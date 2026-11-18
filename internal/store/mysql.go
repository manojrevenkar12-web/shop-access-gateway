package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/manojrevenkar12-web/shop-access-gateway/internal/shop"
)

// Schema of the ledger (MySQL 8 and MariaDB 10.11). Times are UTC with microseconds.
const Schema = `
CREATE TABLE IF NOT EXISTS processed_events (
  provider     VARCHAR(16)  NOT NULL,
  event_id     VARCHAR(64)  NOT NULL,
  processed_at DATETIME(6)  NOT NULL,
  PRIMARY KEY (provider, event_id)
);
CREATE TABLE IF NOT EXISTS subscriptions (
  id                   VARCHAR(64)  NOT NULL PRIMARY KEY,
  provider             VARCHAR(16)  NOT NULL,
  user_id              BIGINT       NOT NULL,
  plan                 VARCHAR(32)  NOT NULL,
  status               VARCHAR(16)  NOT NULL,
  started_at           DATETIME(6)  NOT NULL,
  period_start         DATETIME(6)  NOT NULL,
  period_end           DATETIME(6)  NOT NULL,
  cancel_at_period_end BOOLEAN      NOT NULL,
  ended_at             DATETIME(6)  NULL,
  version              BIGINT       NOT NULL,
  access_until         DATETIME(6)  NOT NULL,
  source               VARCHAR(128) NOT NULL,
  applied_at           DATETIME(6)  NOT NULL,
  KEY user_access (user_id, access_until),
  KEY by_provider (provider)
);
CREATE TABLE IF NOT EXISTS subscription_history (
  seq          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  sub_id       VARCHAR(64)  NOT NULL,
  provider     VARCHAR(16)  NOT NULL,
  user_id      BIGINT       NOT NULL,
  plan         VARCHAR(32)  NOT NULL,
  status       VARCHAR(16)  NOT NULL,
  started_at   DATETIME(6)  NOT NULL,
  period_start DATETIME(6)  NOT NULL,
  period_end   DATETIME(6)  NOT NULL,
  cancel_at_period_end BOOLEAN NOT NULL,
  ended_at     DATETIME(6)  NULL,
  version      BIGINT       NOT NULL,
  access_until DATETIME(6)  NOT NULL,
  source       VARCHAR(128) NOT NULL,
  applied_at   DATETIME(6)  NOT NULL,
  KEY by_sub (sub_id, seq)
);
CREATE TABLE IF NOT EXISTS licences (
  id       VARCHAR(64)  NOT NULL PRIMARY KEY,
  code     VARCHAR(64)  NOT NULL UNIQUE,
  seats    INT          NOT NULL,
  starts   DATETIME(6)  NOT NULL,
  ends     DATETIME(6)  NOT NULL,
  customer VARCHAR(128) NOT NULL
);
CREATE TABLE IF NOT EXISTS seats (
  licence_id VARCHAR(64) NOT NULL,
  user_id    BIGINT      NOT NULL,
  joined_at  DATETIME(6) NOT NULL,
  PRIMARY KEY (licence_id, user_id),
  KEY by_user (user_id)
);`

// MySQL is the ledger on MySQL (or MariaDB).
type MySQL struct{ db *sql.DB }

// OpenMySQL connects, applies the schema and returns the store. dsn is a go-sql-driver DSN without parameters.
func OpenMySQL(ctx context.Context, dsn string) (*MySQL, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ParseTime, cfg.Loc, cfg.MultiStatements = true, time.UTC, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(32)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &MySQL{db: db}, nil
}

// Reset empties every table (tests and benchmarks).
func (s *MySQL) Reset(ctx context.Context) error {
	for _, t := range []string{"processed_events", "subscriptions", "subscription_history", "seats", "licences"} {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return err
		}
	}
	return nil
}

func (s *MySQL) DB() *sql.DB { return s.db }

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

const subCols = `provider, user_id, plan, status, started_at, period_start, period_end, cancel_at_period_end, ended_at,
  version, access_until, source, applied_at`

func subArgs(c Change) []any {
	x := c.Sub
	return []any{string(x.Provider), x.User, x.Plan, string(x.Status), x.StartedAt.UTC(), x.PeriodStart.UTC(),
		x.PeriodEnd.UTC(), x.CancelAtPeriodEnd, nullTime(x.EndedAt), x.Version, c.Until.UTC(), trunc(c.Source, 128),
		c.At.UTC()}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// retry runs a transaction again when the database chose it as a deadlock victim or a lock wait timed out; the
// transaction was rolled back, so running it again is safe.
func retry(fn func() error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		err = fn()
		var me *mysql.MySQLError
		if !errors.As(err, &me) || (me.Number != 1213 && me.Number != 1205) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	return err
}

// Apply writes a change in one transaction: the processed mark, the row lock, the snapshot and its history.
func (s *MySQL) Apply(ctx context.Context, c Change) error {
	return retry(func() error { return s.apply(ctx, c) })
}

func (s *MySQL) apply(ctx context.Context, c Change) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil && !errors.Is(err, ErrStale) {
			_ = tx.Rollback()
		}
	}()
	if c.EventID != "" {
		res, e := tx.ExecContext(ctx, `INSERT IGNORE INTO processed_events (provider, event_id, processed_at)
			VALUES (?, ?, ?)`, string(c.Provider), c.EventID, c.At.UTC())
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrDuplicate
		}
	}
	if c.Sub != nil {
		var version int64
		e := tx.QueryRowContext(ctx, `SELECT version FROM subscriptions WHERE id = ? FOR UPDATE`, c.Sub.ID).Scan(&version)
		exists := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if c.Ordered && exists && version >= c.Sub.Version {
			if e := tx.Commit(); e != nil { // the stale event stays marked processed
				return e
			}
			return ErrStale
		}
		args := append([]any{c.Sub.ID}, subArgs(c)...)
		ph := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
		var upd []string
		for _, col := range strings.Split(subCols, ",") {
			col = strings.TrimSpace(col)
			upd = append(upd, col+" = VALUES("+col+")")
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO subscriptions (id, "+subCols+") VALUES ("+ph+
			") ON DUPLICATE KEY UPDATE "+strings.Join(upd, ", "), args...); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO subscription_history (sub_id, "+subCols+") VALUES ("+ph+")",
			args...); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func scanRecord(sc interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var prov, status string
	var ended sql.NullTime
	err := sc.Scan(&r.Sub.ID, &prov, &r.Sub.User, &r.Sub.Plan, &status, &r.Sub.StartedAt, &r.Sub.PeriodStart,
		&r.Sub.PeriodEnd, &r.Sub.CancelAtPeriodEnd, &ended, &r.Sub.Version, &r.Until, &r.Source, &r.At)
	r.Sub.Provider, r.Sub.Status = shop.Provider(prov), shop.Status(status)
	if ended.Valid {
		r.Sub.EndedAt = ended.Time
	}
	return r, err
}

func (s *MySQL) query(ctx context.Context, q string, args ...any) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *MySQL) Sub(ctx context.Context, id string) (Record, bool, error) {
	rs, err := s.query(ctx, "SELECT id, "+subCols+" FROM subscriptions WHERE id = ?", id)
	if err != nil || len(rs) == 0 {
		return Record{}, false, err
	}
	return rs[0], true, nil
}

func (s *MySQL) SubsOfUser(ctx context.Context, user int64) ([]Record, error) {
	return s.query(ctx, "SELECT id, "+subCols+" FROM subscriptions WHERE user_id = ? ORDER BY id", user)
}

func (s *MySQL) Subs(ctx context.Context, p shop.Provider) ([]Record, error) {
	return s.query(ctx, "SELECT id, "+subCols+" FROM subscriptions WHERE provider = ? ORDER BY id", string(p))
}

func (s *MySQL) History(ctx context.Context, id string) ([]Record, error) {
	return s.query(ctx, "SELECT sub_id, "+subCols+" FROM subscription_history WHERE sub_id = ? ORDER BY seq", id)
}

func (s *MySQL) Access(ctx context.Context, user int64, t time.Time) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM subscriptions WHERE user_id = ? AND started_at <= ? AND access_until > ?
		UNION ALL
		SELECT 1 FROM seats JOIN licences l ON l.id = seats.licence_id
		 WHERE seats.user_id = ? AND l.starts <= ? AND l.ends > ? AND seats.joined_at <= ?
		LIMIT 1`, user, t.UTC(), t.UTC(), user, t.UTC(), t.UTC(), t.UTC()).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *MySQL) PutLicence(ctx context.Context, l Licence) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO licences (id, code, seats, starts, ends, customer) VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE seats = VALUES(seats), starts = VALUES(starts), ends = VALUES(ends)`,
		l.ID, l.Code, l.Seats, l.Starts.UTC(), l.Ends.UTC(), l.Customer)
	return err
}

// JoinCampus takes a seat under the licence row's lock.
func (s *MySQL) JoinCampus(ctx context.Context, code string, user int64, at time.Time) (Licence, error) {
	var l Licence
	err := retry(func() error {
		var e error
		l, e = s.join(ctx, code, user, at)
		return e
	})
	return l, err
}

func (s *MySQL) join(ctx context.Context, code string, user int64, at time.Time) (l Licence, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return l, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	// The licence row is the lock: concurrent joins for one licence queue here, so the count below is exact.
	err = tx.QueryRowContext(ctx, `SELECT id, code, seats, starts, ends, customer FROM licences WHERE code = ? FOR UPDATE`,
		code).Scan(&l.ID, &l.Code, &l.Seats, &l.Starts, &l.Ends, &l.Customer)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrUnknownCode
	}
	if err != nil {
		return l, err
	}
	if at.Before(l.Starts) || !at.Before(l.Ends) {
		return l, ErrNotActive
	}
	var taken, mine int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(user_id = ?), 0) FROM seats WHERE licence_id = ?`,
		user, l.ID).Scan(&taken, &mine); err != nil {
		return l, err
	}
	if mine > 0 {
		return l, tx.Commit()
	}
	if taken >= l.Seats {
		return l, ErrFull
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO seats (licence_id, user_id, joined_at) VALUES (?, ?, ?)`,
		l.ID, user, at.UTC()); err != nil {
		return l, err
	}
	return l, tx.Commit()
}

func (s *MySQL) SeatsOf(ctx context.Context, user int64) ([]Licence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.code, l.seats, l.starts, l.ends, l.customer FROM seats
		JOIN licences l ON l.id = seats.licence_id WHERE seats.user_id = ? ORDER BY l.id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Licence
	for rows.Next() {
		var l Licence
		if err := rows.Scan(&l.ID, &l.Code, &l.Seats, &l.Starts, &l.Ends, &l.Customer); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *MySQL) SeatCount(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM seats WHERE licence_id = ?`, id).Scan(&n)
	return n, err
}

func (s *MySQL) Close() error { return s.db.Close() }
