package launchday;

import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.TimeUnit;

/**
 * Tiny fixed-size JDBC pool. Every unit of work runs in its own transaction on
 * its own connection, so concurrent requests never share a Connection.
 */
final class Db {
    interface Work<T> {
        T run(Connection c) throws Exception;
    }

    private final String url;
    private final String user;
    private final String pass;
    private final BlockingQueue<Connection> idle;

    Db(String url, String user, String pass, int size) throws SQLException {
        this.url = url;
        this.user = user;
        this.pass = pass;
        this.idle = new ArrayBlockingQueue<>(size);
        for (int i = 0; i < size; i++) idle.add(open());
    }

    private Connection open() throws SQLException {
        return DriverManager.getConnection(url, user, pass);
    }

    /** Runs work in a transaction: commit on success, rollback on any exception. */
    <T> T tx(Work<T> work) throws Exception {
        Connection c = idle.poll(5, TimeUnit.SECONDS);
        if (c == null) throw new SQLException("db pool exhausted");
        boolean broken = false;
        try {
            c.setAutoCommit(false);
            T out = work.run(c);
            c.commit();
            return out;
        } catch (Exception e) {
            try {
                c.rollback();
            } catch (SQLException re) {
                broken = true;
            }
            throw e;
        } finally {
            if (broken || c.isClosed()) {
                try {
                    c.close();
                } catch (SQLException ignored) {
                }
                try {
                    c = open();
                } catch (SQLException ignored) {
                    // keep the dead one; the next tx() fails, rolls back and retries the reopen
                }
            }
            idle.add(c);
        }
    }

    static PreparedStatement prepare(Connection c, String sql, Object... args) throws SQLException {
        PreparedStatement ps = c.prepareStatement(sql);
        for (int i = 0; i < args.length; i++) ps.setObject(i + 1, args[i]);
        return ps;
    }

    static int exec(Connection c, String sql, Object... args) throws SQLException {
        try (PreparedStatement ps = prepare(c, sql, args)) {
            return ps.executeUpdate();
        }
    }

    /** Runs a statement for its side effect only (e.g. SELECT pg_advisory_xact_lock(...)). */
    static void run(Connection c, String sql, Object... args) throws SQLException {
        try (PreparedStatement ps = prepare(c, sql, args)) {
            ps.execute();
        }
    }

    /** First column of the first row, or null when there is no row. */
    static Integer intOrNull(Connection c, String sql, Object... args) throws SQLException {
        try (PreparedStatement ps = prepare(c, sql, args); ResultSet rs = ps.executeQuery()) {
            return rs.next() ? rs.getInt(1) : null;
        }
    }
}
