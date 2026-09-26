package launchday;

import com.google.gson.Gson;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;

/**
 * HTTP client for the Central Authority. Any network failure or timeout surfaces as
 * IOException so callers can treat "slow" and "down" the same way.
 */
final class AuthorityClient {
    record Reply(int code, JsonObject body) {
        /** Authority's current available count for the item, or -1 if the reply had none. */
        int available() {
            JsonElement v = body == null ? null : body.get("available");
            return v == null || v.isJsonNull() ? -1 : v.getAsInt();
        }

        String str(String key) {
            JsonElement v = body == null ? null : body.get(key);
            return v == null || v.isJsonNull() ? null : v.getAsString();
        }
    }

    private static final Gson GSON = new Gson();

    private final String base;
    private final Duration callTimeout;
    private final Duration probeTimeout;
    private final HttpClient http;

    AuthorityClient(String base, Duration callTimeout, Duration probeTimeout) {
        this.base = base;
        this.callTimeout = callTimeout;
        this.probeTimeout = probeTimeout;
        this.http = HttpClient.newBuilder()
                .version(HttpClient.Version.HTTP_1_1)
                .connectTimeout(callTimeout)
                .build();
    }

    /** reservationId is ours; the authority dedupes on it, which makes replays safe. */
    Reply reserve(String reservationId, String itemId, String userId, int qty) throws IOException {
        JsonObject body = new JsonObject();
        body.addProperty("reservationId", reservationId);
        body.addProperty("itemId", itemId);
        body.addProperty("userId", userId);
        body.addProperty("qty", qty);
        return send(HttpRequest.newBuilder(URI.create(base + "/reservations"))
                .timeout(callTimeout)
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
                .build());
    }

    Reply item(String itemId) throws IOException {
        return send(HttpRequest.newBuilder(URI.create(base + "/items/" + itemId))
                .timeout(callTimeout).GET().build());
    }

    /** Health probe with a short timeout, so "slow" counts as unhealthy. */
    boolean healthy() {
        try {
            return send(HttpRequest.newBuilder(URI.create(base + "/health"))
                    .timeout(probeTimeout).GET().build()).code() == 200;
        } catch (IOException e) {
            return false;
        }
    }

    private Reply send(HttpRequest req) throws IOException {
        try {
            HttpResponse<String> resp = http.send(req, HttpResponse.BodyHandlers.ofString());
            JsonObject json = null;
            try {
                json = GSON.fromJson(resp.body(), JsonObject.class);
            } catch (RuntimeException ignored) {
            }
            return new Reply(resp.statusCode(), json);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IOException("interrupted", e);
        }
    }
}
