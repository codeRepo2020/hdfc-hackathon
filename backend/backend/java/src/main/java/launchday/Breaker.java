package launchday;

import java.io.IOException;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.locks.ReentrantLock;

/**
 * Owns the live/standin mode, the health poller that flips it, and the replayer
 * that drains the stand-in queue once the authority is healthy again.
 *
 * Mode gate: stand-in requests hold pg_advisory_xact_lock_shared(1,1) while they
 * enqueue; flipping back to live takes it exclusively and only succeeds if the
 * queue is empty. So no reservation can be enqueued "behind" the switch to live.
 */
final class Breaker {
    static final String GATE_SHARED = "SELECT pg_advisory_xact_lock_shared(1, 1)";
    private static final String GATE_EXCLUSIVE = "SELECT pg_advisory_xact_lock(1, 1)";

    record ReplayStats(int replayed, int confirmed, int reversed, boolean drained) {}

    private final Db db;
    private final AuthorityClient authority;
    private final int pollMs;
    private final int healthyChecks;

    private volatile boolean live = true;
    private volatile boolean authorityUp = true;
    private final AtomicInteger healthyStreak = new AtomicInteger();
    private final ReentrantLock replayLock = new ReentrantLock();

    Breaker(Db db, AuthorityClient authority, int pollMs, int healthyChecks) {
        this.db = db;
        this.authority = authority;
        this.pollMs = pollMs;
        this.healthyChecks = healthyChecks;
    }

    boolean isLive() {
        return live;
    }

    boolean authorityUp() {
        return authorityUp;
    }

    void trip() {
        healthyStreak.set(0);
        authorityUp = false;
        if (live) {
            live = false;
            System.out.println("mode -> standin (authority unreachable or slow)");
        }
    }

    void start() throws Exception {
        int queued = db.tx(c -> Db.intOrNull(c, "SELECT COUNT(*) FROM standin_queue WHERE done_at IS NULL"));
        if (queued > 0 || !authority.healthy()) trip();
        ScheduledExecutorService poller = Executors.newSingleThreadScheduledExecutor(r -> {
            Thread t = new Thread(r, "authority-poller");
            t.setDaemon(true);
            return t;
        });
        poller.scheduleWithFixedDelay(this::poll, pollMs, pollMs, TimeUnit.MILLISECONDS);
    }

    private void poll() {
        try {
            if (!authority.healthy()) {
                trip();
                return;
            }
            authorityUp = true;
            if (!live && healthyStreak.incrementAndGet() >= healthyChecks) recover();
        } catch (Throwable t) {
            System.err.println("poller: " + t);
        }
    }

    ReplayStats recover() throws Exception {
        replayLock.lock();
        try {
            int replayed = 0, confirmed = 0, reversed = 0;
            if (!authority.healthy()) {
                trip();
                return new ReplayStats(0, 0, 0, false);
            }
            while (true) {
                ReplayStats s = drain();
                replayed += s.replayed();
                confirmed += s.confirmed();
                reversed += s.reversed();
                if (!s.drained()) return new ReplayStats(replayed, confirmed, reversed, false);
                if (tryGoLive()) return new ReplayStats(replayed, confirmed, reversed, true);
                // someone enqueued while we drained: go round again
            }
        } finally {
            replayLock.unlock();
        }
    }

    /** Forces mode from outside (used by /admin/reset). */
    void resetMode() {
        replayLock.lock();
        try {
            healthyStreak.set(0);
            if (authority.healthy()) {
                authorityUp = true;
                live = true;
            } else {
                trip();
            }
        } finally {
            replayLock.unlock();
        }
    }

    private boolean tryGoLive() throws Exception {
        return db.tx(c -> {
            Db.run(c, GATE_EXCLUSIVE); // waits for in-flight stand-in enqueues to commit
            int left = Db.intOrNull(c, "SELECT COUNT(*) FROM standin_queue WHERE done_at IS NULL");
            if (left > 0) return false;
            live = true; // flipped while holding the gate, so no one can enqueue behind us
            System.out.println("mode -> live (queue drained)");
            return true;
        });
    }

    private record Queued(long seq, String reservationId, String itemId, String userId, int qty) {}

    private ReplayStats drain() throws Exception {
        int replayed = 0, confirmed = 0, reversed = 0;
        while (true) {
            Queued q = db.tx(c -> {
                try (PreparedStatement ps = Db.prepare(c,
                        "SELECT seq, reservation_id, item_id, user_id, qty FROM standin_queue "
                                + "WHERE done_at IS NULL ORDER BY seq LIMIT 1");
                     ResultSet rs = ps.executeQuery()) {
                    return rs.next()
                            ? new Queued(rs.getLong(1), rs.getString(2), rs.getString(3), rs.getString(4), rs.getInt(5))
                            : null;
                }
            });
            if (q == null) return new ReplayStats(replayed, confirmed, reversed, true);

            AuthorityClient.Reply reply;
            try {
                // Same reservationId as the original request: if the authority already
                // applied it (e.g. our call timed out but it went through), it returns
                // the existing reservation instead of reserving twice.
                reply = authority.reserve(q.reservationId(), q.itemId(), q.userId(), q.qty());
            } catch (IOException e) {
                trip();
                return new ReplayStats(replayed, confirmed, reversed, false);
            }
            String outcome;
            String reason = null;
            if (reply.code() == 201) {
                outcome = "confirmed";
            } else if (reply.code() >= 400 && reply.code() < 500) {
                outcome = "reversed";
                reason = reply.str("reason") != null ? reply.str("reason") : "authority_refused";
            } else {
                trip(); // 5xx: authority is not really healthy, try again later
                return new ReplayStats(replayed, confirmed, reversed, false);
            }

            String finalReason = reason;
            db.tx(c -> {
                int moved = Db.exec(c, "UPDATE reservations SET status=? WHERE id=? AND status='pending'",
                        outcome, q.reservationId());
                if (moved == 1 && finalReason != null) {
                    Db.exec(c, "UPDATE reservation_meta SET reason=? WHERE reservation_id=?",
                            finalReason, q.reservationId());
                }
                Db.exec(c, "UPDATE standin_queue SET done_at=now(), outcome=? WHERE seq=?", outcome, q.seq());
                if (reply.available() >= 0) App.syncShadow(c, q.itemId(), null, reply.available(), false);
                return null;
            });
            System.out.println("replayed " + q.reservationId() + " -> " + outcome);
            replayed++;
            if (outcome.equals("confirmed")) confirmed++;
            else reversed++;
        }
    }
}
