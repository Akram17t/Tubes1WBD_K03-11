package telemetry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// QueryTracer dipasang di config koneksi pgx, otomatis dipanggil pgx setiap ada query.
// Database cuma label ("platform" / "target") biar log kebaca query-nya ke DB mana
type QueryTracer struct {
	Database string
}

type queryStartKey struct{}

// data yang dibawa dari awal query sampai selesai
type queryStart struct {
	sql     string
	args    []any
	started time.Time
	span    trace.Span
}

// dipanggil pgx sebelum query jalan: mulai span sql.query dan catat waktu mulai
func (t QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := Tracer().Start(ctx, "sql.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.label", t.Database),
			attribute.String("db.statement", truncate(compactSQL(data.SQL), maxLoggedQueryLen)),
		))
	return context.WithValue(ctx, queryStartKey{}, &queryStart{sql: data.SQL, args: queryArgs(data.Args), started: time.Now(), span: span})
}

// buang argumen internal yang disisipin pgx (misal QueryResultFormatsByOID), sisanya parameter asli query
func queryArgs(args []any) []any {
	for len(args) > 0 {
		switch args[0].(type) {
		case pgx.QueryResultFormats, pgx.QueryResultFormatsByOID, pgx.QueryExecMode, pgx.QueryRewriter:
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

// dipanggil pgx setelah query selesai: tulis log JSON (parameter disamarkan) lalu tutup span
func (t QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(queryStartKey{}).(*queryStart)
	if !ok {
		return
	}
	LogSQL(ctx, SQLEvent{
		Event:     "sql.query",
		Query:     start.sql,
		Args:      start.args,
		Started:   start.started,
		Rows:      data.CommandTag.RowsAffected(),
		RowsKnown: data.Err == nil,
		Err:       data.Err,
		Attrs:     []any{"db", t.Database},
	})

	if data.Err != nil {
		// pakai SafeError biar detail error PostgreSQL (bisa berisi nilai row) ga masuk span
		start.span.SetStatus(codes.Error, SafeError(data.Err))
		if code := SQLState(data.Err); code != "" {
			start.span.SetAttributes(attribute.String("db.sqlstate", code))
		}
	} else {
		start.span.SetAttributes(attribute.Int64("db.rows_affected", data.CommandTag.RowsAffected()))
	}
	start.span.End()
}
