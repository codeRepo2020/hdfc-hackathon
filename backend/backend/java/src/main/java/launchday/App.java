package launchday;

import com.google.gson.Gson;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.Executors;

/**
 * Reservation API (Java).
 *
 *  Part A — Idempotency: (userId, Idempotency-Key) is serialized with a Postgres
 *  advisory lock and mapped to exactly one reservation in idempotency_keys.
 *
 *  Breaker: when the authority is slow/down we stop calling it and fail fast
 *  (503) until the health poller sees it healthy again. Still no stand-in.
 */
public class App {
    private static final Gson GSON = new Gson();

    private static final String SCHEMA = """
            CREATE TABLE IF NOT EXISTS idempotency_keys (
                user_id        TEXT NOT NULL,
                idem_key       TEXT NOT NULL,
                body_hash      TEXT NOT NULL,
                reservation_id TEXT NOT NULL,
                created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
                PRIMARY KEY (user_id, idem_key)
            );
            """;

    private static final String SELECT_RESERVATION = """
            SELECT r.id, r.item_id, r.user_id, r.qty, r.status
            FROM reservations r
            """;

    private final Db db;
    private final AuthorityClient authority;
    private final Breaker breaker;

    App(Db db, AuthorityClient authority, Breaker breaker) {
        this.db = db;
        this.authority = authority;
        this.breaker = breaker;
    }

    public static void main(String[] args) throws Exception {
        String[] conn = jdbc(env("DATABASE_URL", "jdbc:postgresql://localhost:5432/student"),
                env("DATABASE_USER", "launchday"), env("DATABASE_PASSWORD", "launchday"));
        Db db = new Db(conn[0], conn[1], conn[2], Integer.parseInt(env("DB_POOL_SIZE", "30")));
        db.tx(c -> {
            try (Statement st = c.createStatement()) {
                st.execute(SCHEMA);
            }
            return null;
        });

        AuthorityClient authority = new AuthorityClient(
                env("AUTHORITY_URL", "http://127.0.0.1:9000").replaceAll("/$", ""),
                Duration.ofMillis(Integer.parseInt(env("AUTHORITY_TIMEOUT_MS", "2000"))),
                Duration.ofMillis(Integer.parseInt(env("AUTHORITY_PROBE_TIMEOUT_MS", "800"))));
        Breaker breaker = new Breaker(authority,
                Integer.parseInt(env("AUTHORITY_BREAKER_POLL_MS", "1000")),
                Integer.parseInt(env("AUTHORITY_BREAKER_HEALTHY_CHECKS", "3")));
        App app = new App(db, authority, breaker);
        breaker.start();

        int port = Integer.parseInt(env("PORT", "8080"));
        HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);
        server.createContext("/health", guard(app::health));
        server.createContext("/items", guard(app::items));
        server.createContext("/reservations", guard(app::reservations));
        server.createContext("/admin/reset", guard(app::reset));
        // Default HttpServer handles one request at a time; give each request its own thread.
        server.setExecutor(Executors.newVirtualThreadPerTaskExecutor());
        server.start();
        System.out.println("reservation api (java) :" + port + " mode=" + (breaker.isLive() ? "live" : "standin"));
    }

    // ---------------------------------------------------------------- handlers

    private void health(HttpExchange ex) throws IOException {
        Map<String, Object> out = new LinkedHashMap<>();
        out.put("status", "ok");
        out.put("authority", breaker.authorityUp() ? "healthy" : "down");
        out.put("mode", breaker.isLive() ? "live" : "standin");
        write(ex, 200, out);
    }

    private void items(HttpExchange ex) throws Exception {
        String id = ex.getRequestURI().getPath().replaceFirst("^/items/?", "");
        if (id.isEmpty()) {
            write(ex, 404, Map.of("error", "not_found"));
            return;
        }
        if (breaker.isLive()) {
            try {
                AuthorityClient.Reply r = authority.item(id);
                write(ex, r.code(), r.body() == null ? Map.of() : r.body());
                return;
            } catch (IOException e) {
                breaker.trip();
            }
        }
        write(ex, 503, Map.of("error", "authority_unavailable"));
    }

    private void reservations(HttpExchange ex) throws Exception {
        String path = ex.getRequestURI().getPath();
        String method = ex.getRequestMethod();
        if ("POST".equals(method) && "/reservations".equals(path)) {
            postReserve(ex);
        } else if ("GET".equals(method) && path.startsWith("/reservations/") && path.length() > "/reservations/".length()) {
            getOne(ex, path.substring("/reservations/".length()));
        } else if ("GET".equals(method)) {
            list(ex);
        } else {
            write(ex, 405, Map.of("error", "method"));
        }
    }

    private void reset(HttpExchange ex) throws Exception {
        db.tx(c -> {
            Db.exec(c, "DELETE FROM idempotency_keys");
            Db.exec(c, "DELETE FROM reservations");
            return null;
        });
        breaker.resetMode();
        write(ex, 200, Map.of("status", "reset"));
    }

    // ---------------------------------------------------------------- POST /reservations

    private void postReserve(HttpExchange ex) throws Exception {
        JsonObject req;
        try {
            req = GSON.fromJson(new String(ex.getRequestBody().readAllBytes(), StandardCharsets.UTF_8), JsonObject.class);
        } catch (RuntimeException e) {
            req = null;
        }
        String itemId = str(req, "itemId");
        String userId = str(req, "userId");
        int qty = 1;
        boolean badQty = false;
        try {
            if (req != null && req.has("qty") && !req.get("qty").isJsonNull()) qty = req.get("qty").getAsInt();
        } catch (RuntimeException e) {
            badQty = true;
        }
        if (badQty || itemId == null || itemId.isEmpty() || userId == null || userId.isEmpty()) {
            write(ex, 400, Map.of("error", "invalid_body"));
            return;
        }
        if (qty <= 0) qty = 1;
        String key = ex.getRequestHeaders().getFirst("Idempotency-Key");
        if (key != null && key.isBlank()) key = null;

        String item = itemId, user = userId, idemKey = key;
        int n = qty;
        // userId is the key's scope, so only the rest of the body is fingerprinted
        String hash = sha256(item + "\n" + n);
        Map<String, Object> result = db.tx(c -> reserve(c, user, idemKey, hash, item, n));
        write(ex, httpCode(result), result);
    }

    /**
     * Runs inside one transaction. Returns the response body; an "error" key means 422.
     */
    private Map<String, Object> reserve(Connection c, String userId, String key, String hash,
                                        String itemId, int qty) throws Exception {
        if (key != null) {
            // Serializes every request for this (user, key): concurrent replays wait here,
            // then see the row the first one committed.
            // length prefix keeps ("a:b","c") and ("a","b:c") from colliding
            Db.run(c, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", userId.length() + ":" + userId + ":" + key);
            String existingHash = null, existingId = null;
            try (PreparedStatement ps = Db.prepare(c,
                    "SELECT body_hash, reservation_id FROM idempotency_keys WHERE user_id=? AND idem_key=?", userId, key);
                 ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    existingHash = rs.getString(1);
                    existingId = rs.getString(2);
                }
            }
            if (existingId != null) {
                if (!existingHash.equals(hash)) {
                    return Map.of("error", "idempotency_key_reused",
                            "message", "Idempotency-Key was already used with a different request body");
                }
                return loadReservation(c, existingId); // replay: same id, current status
            }
        }

        // Breaker open: don't wait 2s on a timeout, fail fast.
        // (Nothing has been written yet, so the transaction commits empty.)
        if (!breaker.isLive()) return Map.of("error", "authority_unavailable");

        String rid = UUID.randomUUID().toString();
        AuthorityClient.Reply r;
        try {
            r = authority.reserve(rid, itemId, userId, qty);
        } catch (IOException e) {
            breaker.trip();
            return Map.of("error", "authority_unavailable");
        }
        if (r.code() >= 500) {
            breaker.trip();
            return Map.of("error", "authority_unavailable");
        }
        String status = r.code() == 201 ? "confirmed" : "rejected";
        String reason = status.equals("rejected") ? r.str("reason") : null;

        Db.exec(c, "INSERT INTO reservations (id, item_id, user_id, qty, status) VALUES (?,?,?,?,?)",
                rid, itemId, userId, qty, status);
        if (key != null) {
            Db.exec(c, "INSERT INTO idempotency_keys (user_id, idem_key, body_hash, reservation_id) VALUES (?,?,?,?)",
                    userId, key, hash, rid);
        }
        return body(rid, itemId, userId, qty, status, "live", reason);
    }

    // ---------------------------------------------------------------- reads

    private void getOne(HttpExchange ex, String id) throws Exception {
        Map<String, Object> r = db.tx(c -> loadReservation(c, id));
        if (r == null) write(ex, 404, Map.of("error", "not_found"));
        else write(ex, 200, r);
    }

    private void list(HttpExchange ex) throws Exception {
        String user = query(ex, "userId");
        List<Map<String, Object>> out = db.tx(c -> {
            List<Map<String, Object>> rows = new ArrayList<>();
            try (PreparedStatement ps = user == null
                    ? Db.prepare(c, SELECT_RESERVATION + " ORDER BY r.created_at")
                    : Db.prepare(c, SELECT_RESERVATION + " WHERE r.user_id=? ORDER BY r.created_at", user);
                 ResultSet rs = ps.executeQuery()) {
                while (rs.next()) rows.add(row(rs));
            }
            return rows;
        });
        write(ex, 200, out);
    }

    private static Map<String, Object> loadReservation(Connection c, String id) throws SQLException {
        try (PreparedStatement ps = Db.prepare(c, SELECT_RESERVATION + " WHERE r.id=?", id);
             ResultSet rs = ps.executeQuery()) {
            return rs.next() ? row(rs) : null;
        }
    }

    private static Map<String, Object> row(ResultSet rs) throws SQLException {
        return body(rs.getString(1), rs.getString(2), rs.getString(3), rs.getInt(4),
                rs.getString(5), "live", null);
    }

    private static Map<String, Object> body(String id, String itemId, String userId, int qty,
                                            String status, String mode, String reason) {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("reservationId", id);
        m.put("itemId", itemId);
        m.put("userId", userId);
        m.put("qty", qty);
        m.put("status", status);
        m.put("mode", mode);
        if (reason != null) m.put("reason", reason);
        return m;
    }

    private static int httpCode(Map<String, Object> result) {
        if ("authority_unavailable".equals(result.get("error"))) return 503;
        if (result.containsKey("error")) return 422;
        return switch (String.valueOf(result.get("status"))) {
            case "confirmed" -> 201;
            case "pending" -> 202;
            default -> 409; // rejected, reversed
        };
    }

    // ---------------------------------------------------------------- plumbing

    private interface Handler {
        void handle(HttpExchange ex) throws Exception;
    }

    /** CORS preflight + turns any uncaught exception into a JSON 500 instead of a dropped connection. */
    private static HttpHandler guard(Handler h) {
        return ex -> {
            try {
                ex.getResponseHeaders().add("Access-Control-Allow-Origin", "*");
                ex.getResponseHeaders().add("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key");
                ex.getResponseHeaders().add("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
                if ("OPTIONS".equals(ex.getRequestMethod())) {
                    ex.sendResponseHeaders(204, -1);
                    return;
                }
                h.handle(ex);
            } catch (Exception e) {
                System.err.println(ex.getRequestMethod() + " " + ex.getRequestURI() + ": " + e);
                try {
                    write(ex, 500, Map.of("error", "internal"));
                } catch (IOException ignored) {
                    // headers already sent
                }
            } finally {
                ex.close();
            }
        };
    }

    private static void write(HttpExchange ex, int code, Object body) throws IOException {
        byte[] b = GSON.toJson(body).getBytes(StandardCharsets.UTF_8);
        ex.getResponseHeaders().set("Content-Type", "application/json");
        ex.sendResponseHeaders(code, b.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(b);
        }
    }

    private static String str(JsonObject o, String key) {
        if (o == null) return null;
        JsonElement v = o.get(key);
        return v == null || v.isJsonNull() || !v.isJsonPrimitive() ? null : v.getAsString();
    }

    private static String sha256(String s) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(s.getBytes(StandardCharsets.UTF_8)));
    }

    private static String query(HttpExchange ex, String key) {
        String q = ex.getRequestURI().getQuery();
        if (q == null || q.isEmpty()) return null;
        for (String part : q.split("&")) {
            String[] kv = part.split("=", 2);
            if (kv.length == 2 && kv[0].equals(key) && !kv[1].isEmpty()) return kv[1];
        }
        return null;
    }

    private static String env(String k, String def) {
        String v = System.getenv(k);
        return v == null || v.isEmpty() ? def : v;
    }

    /** Accepts jdbc:postgresql://… or the postgres://user:pass@host/db form the kit's scripts use. */
    private static String[] jdbc(String url, String user, String pass) {
        if (url.startsWith("jdbc:")) return new String[]{url, user, pass};
        URI u = URI.create(url);
        if (u.getUserInfo() != null) {
            String[] up = u.getUserInfo().split(":", 2);
            user = up[0];
            if (up.length == 2) pass = up[1];
        }
        String q = u.getQuery() == null ? "" : "?" + u.getQuery();
        int port = u.getPort() == -1 ? 5432 : u.getPort();
        return new String[]{"jdbc:postgresql://" + u.getHost() + ":" + port + u.getPath() + q, user, pass};
    }
}
