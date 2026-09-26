package launchday;

import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Owns the live/standin mode and the health poller that flips it.
 *
 * live    = requests go straight to the authority.
 * standin = the authority is slow or down; requests do not call it at all.
 */
final class Breaker {
    private final AuthorityClient authority;
    private final int pollMs;
    private final int healthyChecks;

    private volatile boolean live = true;
    private volatile boolean authorityUp = true;
    private final AtomicInteger healthyStreak = new AtomicInteger();

    Breaker(AuthorityClient authority, int pollMs, int healthyChecks) {
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

    /** Called on any failed authority call, and by the poller on a failed probe. */
    void trip() {
        healthyStreak.set(0);
        authorityUp = false;
        if (live) {
            live = false;
            System.out.println("mode -> standin (authority unreachable or slow)");
        }
    }

    void start() {
        if (!authority.healthy()) trip();
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

    /** Several healthy probes in a row: go back to live. */
    void recover() {
        live = true;
        System.out.println("mode -> live");
    }

    /** Forces mode from outside (used by /admin/reset). */
    void resetMode() {
        healthyStreak.set(0);
        if (authority.healthy()) {
            authorityUp = true;
            live = true;
        } else {
            trip();
        }
    }
}
