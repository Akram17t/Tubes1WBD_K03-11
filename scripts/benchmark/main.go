// Command benchmark prepares and measures an isolated target dataset for M2.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
)

const databaseName = "wbd_batch_benchmark"
const updateSQL = "UPDATE public.batch_demo SET value = value + 1"

type result struct {
	Action        string            `json:"action"`
	MeasuredAt    string            `json:"measured_at"`
	Database      string            `json:"database"`
	PostgreSQL    string            `json:"postgresql"`
	Client        string            `json:"client"`
	Settings      map[string]string `json:"settings"`
	Rows          int64             `json:"rows"`
	Verified      bool              `json:"verified"`
	TableBytes    int64             `json:"table_bytes"`
	Query         string            `json:"query,omitempty"`
	UpdateMS      float64           `json:"update_ms,omitempty"`
	CommitMS      float64           `json:"commit_ms,omitempty"`
	OverOneSecond bool              `json:"over_one_second"`
}

func main() {
	action := flag.String("action", "run", "generate, reset, or run")
	rows := flag.Int64("rows", 1_000_000, "rows for generate (1..100000000)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *action, *rows); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validate(action string, rows int64, database string) error {
	if action != "generate" && action != "reset" && action != "run" {
		return errors.New("action must be generate, reset, or run")
	}
	if rows < 1 || rows > 100_000_000 {
		return errors.New("rows must be between 1 and 100000000")
	}
	if database != databaseName {
		return fmt.Errorf("only database %s is allowed", databaseName)
	}
	return nil
}

func run(ctx context.Context, action string, rows int64) error {
	dsn := os.Getenv("BENCHMARK_DATABASE_URL")
	if dsn == "" {
		return errors.New("set BENCHMARK_DATABASE_URL; this command does not read .env")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid BENCHMARK_DATABASE_URL")
	}
	if err := validate(action, rows, config.Database); err != nil {
		return err
	}
	config.ConnectTimeout = 5 * time.Second
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return errors.New("could not connect to benchmark database; check connection settings")
	}
	defer conn.Close(context.Background())
	var actualDatabase string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&actualDatabase); err != nil {
		return err
	}
	if err := validate(action, rows, actualDatabase); err != nil {
		return err
	}
	r := result{Action: action, Database: actualDatabase, MeasuredAt: time.Now().UTC().Format(time.RFC3339),
		Client: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), Settings: map[string]string{}}
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&r.PostgreSQL); err != nil {
		return err
	}
	for _, setting := range []string{"shared_buffers", "fsync", "synchronous_commit", "full_page_writes"} {
		var value string
		if err := conn.QueryRow(ctx, "SELECT current_setting($1)", setting).Scan(&value); err != nil {
			return err
		}
		r.Settings[setting] = value
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if action == "generate" {
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.batch_demo (
			id BIGINT PRIMARY KEY CHECK (id > 0), value BIGINT NOT NULL)`); err != nil {
			return err
		}
	}
	// Hold the lock through verification and commit so another writer cannot corrupt the measurement.
	if _, err := tx.Exec(ctx, "LOCK TABLE public.batch_demo IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	if action == "reset" || action == "run" {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM public.batch_demo").Scan(&rows); err != nil {
			return err
		}
		if rows == 0 {
			return errors.New("dataset is empty; run generate first")
		}
	}
	r.Rows = rows
	expectedValue := int64(0)
	if action == "run" {
		if err := verify(ctx, tx, rows, 0); err != nil {
			return fmt.Errorf("baseline must have value=0 for every row; run reset first: %w", err)
		}
		r.Query = updateSQL
		start := time.Now()
		tag, err := tx.Exec(ctx, updateSQL)
		r.UpdateMS = float64(time.Since(start)) / float64(time.Millisecond)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != rows {
			return errors.New("UPDATE row count differs from dataset")
		}
		r.OverOneSecond = r.UpdateMS > 1000
		expectedValue = 1
	} else {
		// Rebuild instead of UPDATE-to-zero so reset also removes dead tuples from the previous run.
		if _, err := tx.Exec(ctx, "TRUNCATE TABLE public.batch_demo"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.batch_demo (id, value)
			SELECT id, 0 FROM generate_series(1::bigint, $1::bigint) AS id`, rows); err != nil {
			return err
		}
	}
	if err := verify(ctx, tx, rows, expectedValue); err != nil {
		return err
	}
	start := time.Now()
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit failed; outcome may be unknown, inspect dataset before continuing")
	}
	if action == "run" {
		r.CommitMS = float64(time.Since(start)) / float64(time.Millisecond)
	} else if _, err := conn.Exec(ctx, "VACUUM (ANALYZE) public.batch_demo"); err != nil {
		return err
	}
	if err := conn.QueryRow(ctx, "SELECT pg_total_relation_size('public.batch_demo')").Scan(&r.TableBytes); err != nil {
		return err
	}
	r.Verified = true
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		return err
	}
	if action == "run" && !r.OverOneSecond {
		return errors.New("UPDATE did not exceed 1 second; generate more rows and measure again")
	}
	return nil
}

func verify(ctx context.Context, tx pgx.Tx, rows, value int64) error {
	var count, matching, minID, maxID int64
	err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE value = $1),
		COALESCE(min(id), 0), COALESCE(max(id), 0) FROM public.batch_demo`, value).
		Scan(&count, &matching, &minID, &maxID)
	if err != nil {
		return err
	}
	if count != rows || matching != rows || minID != 1 || maxID != rows {
		return errors.New("dataset verification failed (count, values, or contiguous IDs)")
	}
	return nil
}
