package launchday

import com.google.gson.Gson
import com.google.gson.JsonObject
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.sql.DriverManager
import java.time.Duration
import java.util.UUID

/**
 * Student Reservation API (Kotlin). Same two launch-day bugs as the Go starter.
 */
fun main() {
    val url = env("DATABASE_URL", "jdbc:postgresql://localhost:5432/student")
    val user = env("DATABASE_USER", "launchday")
    val pass = env("DATABASE_PASSWORD", "launchday")
    val db = DriverManager.getConnection(url, user, pass)
    val app = App(db, env("AUTHORITY_URL", "http://127.0.0.1:9000").trimEnd('/'))
    val port = env("PORT", "8080").toInt()
    val server = HttpServer.create(InetSocketAddress(port), 0)
    server.createContext("/health") { app.health(it) }
    server.createContext("/items") { app.items(it) }
    server.createContext("/reservations") { app.reservations(it) }
    server.createContext("/admin/reset") { app.reset(it) }
    server.createContext("/admin/reconcile") { app.reconcile(it) }
    server.start()
    println("reservation api (kotlin student) :$port")
}

class App(private val db: java.sql.Connection, private val authority: String) {
    private val gson = Gson()
    private val client = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(2)).build()

    fun health(ex: HttpExchange) {
        if (preflight(ex)) return
        write(ex, 200, mapOf("status" to "ok", "authority" to "healthy", "mode" to "live"))
    }

    fun items(ex: HttpExchange) {
        if (preflight(ex)) return
        val id = ex.requestURI.path.removePrefix("/items/")
        try {
            val resp = client.send(
                HttpRequest.newBuilder(URI.create("$authority/items/$id")).GET().timeout(Duration.ofSeconds(2)).build(),
                HttpResponse.BodyHandlers.ofString()
            )
            raw(ex, resp.statusCode(), resp.body())
        } catch (_: Exception) {
            write(ex, 502, mapOf("error" to "authority_unreachable"))
        }
    }

    fun reservations(ex: HttpExchange) {
        if (preflight(ex)) return
        val path = ex.requestURI.path
        when {
            ex.requestMethod == "POST" && path == "/reservations" -> postReserve(ex)
            ex.requestMethod == "GET" && path.startsWith("/reservations/") ->
                getOne(ex, path.removePrefix("/reservations/"))
            ex.requestMethod == "GET" -> list(ex)
            else -> write(ex, 405, mapOf("error" to "method"))
        }
    }

    private fun postReserve(ex: HttpExchange) {
        val req = gson.fromJson(ex.requestBody.bufferedReader().readText(), JsonObject::class.java)
        val itemId = req.get("itemId").asString
        val userId = req.get("userId").asString
        val qty = if (req.has("qty")) req.get("qty").asInt else 1
        val rid = UUID.randomUUID().toString()
        val body = JsonObject().apply {
            addProperty("reservationId", rid)
            addProperty("itemId", itemId)
            addProperty("userId", userId)
            addProperty("qty", qty)
        }
        try {
            val resp = client.send(
                HttpRequest.newBuilder(URI.create("$authority/reservations"))
                    .timeout(Duration.ofSeconds(2))
                    .header("Content-Type", "application/json")
                    .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
                    .build(),
                HttpResponse.BodyHandlers.ofString()
            )
            val status = if (resp.statusCode() == 201) "confirmed" else "rejected"
            db.prepareStatement("INSERT INTO reservations (id, item_id, user_id, qty, status) VALUES (?,?,?,?,?)").use { ps ->
                ps.setString(1, rid)
                ps.setString(2, itemId)
                ps.setString(3, userId)
                ps.setInt(4, qty)
                ps.setString(5, status)
                ps.executeUpdate()
            }
            write(ex, if (status == "confirmed") 201 else 409, mapOf("reservationId" to rid, "status" to status, "mode" to "live"))
        } catch (_: Exception) {
            write(ex, 502, mapOf("error" to "authority_unreachable"))
        }
    }

    private fun getOne(ex: HttpExchange, id: String) {
        db.prepareStatement("SELECT item_id, user_id, qty, status FROM reservations WHERE id=?").use { ps ->
            ps.setString(1, id)
            val rs = ps.executeQuery()
            if (!rs.next()) {
                write(ex, 404, mapOf("error" to "not_found"))
                return
            }
            write(ex, 200, mapOf(
                "reservationId" to id,
                "itemId" to rs.getString(1),
                "userId" to rs.getString(2),
                "qty" to rs.getInt(3),
                "status" to rs.getString(4),
                "mode" to "live"
            ))
        }
    }

    fun list(ex: HttpExchange) {
        val user = query(ex, "userId")
        db.prepareStatement("SELECT id, item_id, user_id, qty, status FROM reservations WHERE (? IS NULL OR user_id=?) ORDER BY created_at").use { ps ->
            ps.setString(1, user)
            ps.setString(2, user)
            val rs = ps.executeQuery()
            val list = mutableListOf<Map<String, Any>>()
            while (rs.next()) {
                list += mapOf(
                    "reservationId" to rs.getString(1),
                    "itemId" to rs.getString(2),
                    "userId" to rs.getString(3),
                    "qty" to rs.getInt(4),
                    "status" to rs.getString(5),
                    "mode" to "live"
                )
            }
            write(ex, 200, list)
        }
    }

    fun reset(ex: HttpExchange) {
        if (preflight(ex)) return
        db.createStatement().executeUpdate("DELETE FROM reservations")
        write(ex, 200, mapOf("status" to "reset"))
    }

    fun reconcile(ex: HttpExchange) {
        if (preflight(ex)) return
        write(ex, 200, mapOf("replayed" to 0, "confirmed" to 0, "reversed" to 0))
    }

    private fun write(ex: HttpExchange, code: Int, body: Any) {
        val b = gson.toJson(body).toByteArray()
        ex.responseHeaders.set("Content-Type", "application/json")
        ex.sendResponseHeaders(code, b.size.toLong())
        ex.responseBody.use { it.write(b) }
    }

    private fun raw(ex: HttpExchange, code: Int, body: String) {
        val b = body.toByteArray()
        ex.responseHeaders.set("Content-Type", "application/json")
        ex.sendResponseHeaders(code, b.size.toLong())
        ex.responseBody.use { it.write(b) }
    }

    private fun preflight(ex: HttpExchange): Boolean {
        ex.responseHeaders.add("Access-Control-Allow-Origin", "*")
        ex.responseHeaders.add("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
        ex.responseHeaders.add("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        if (ex.requestMethod == "OPTIONS") {
            ex.sendResponseHeaders(204, -1)
            return true
        }
        return false
    }

    private fun query(ex: HttpExchange, key: String): String? {
        val q = ex.requestURI.query ?: return null
        return q.split("&").map { it.split("=", limit = 2) }.firstOrNull { it.size == 2 && it[0] == key }?.get(1)
    }
}

fun env(k: String, def: String) = System.getenv(k)?.takeIf { it.isNotEmpty() } ?: def
