package client

// End-to-end version of the telemetry-drain regression test, against a *real*
// engine. The unit tests in client_test.go replace the engine with a fake
// http.RoundTripper; this one proves the real contract holds: Close blocks
// until the engine's telemetry drain finishes, and teardown never starts
// while a span exporter is still receiving data.
//
// It needs internal access (closeCtx), so it lives in package client rather
// than core/integration, and it skips unless _EXPERIMENTAL_DAGGER_RUNNER_HOST
// points at an engine (the engine-dev harness in toolchains/engine-dev sets
// this; locally: _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://<name>).

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestCloseWaitsForRealEngineTelemetryDrain(t *testing.T) {
	runnerHost := os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST")
	if runnerHost == "" {
		t.Skip("requires an engine; set _EXPERIMENTAL_DAGGER_RUNNER_HOST")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The engine streams telemetry per trace (pubsub topics are keyed by
	// trace ID), so the client must run inside a real trace or it subscribes
	// to trace ID zero and receives nothing.
	ctx = telemetry.Init(ctx, telemetry.Config{Detect: false})
	defer telemetry.Close()
	ctx, span := otel.Tracer(InstrumentationLibrary).Start(ctx, t.Name())
	defer span.End()

	exporter := &blockingSpanExporter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	c, err := Connect(ctx, Params{
		RunnerHost:  runnerHost,
		Version:     engine.Version,
		EngineTrace: exporter,
	})
	require.NoError(t, err, "connect to engine")
	exporter.closeCtx.Store(&c.closeCtx)
	t.Cleanup(func() {
		exporter.releaseOnce.Do(func() { close(exporter.release) })
		_ = c.Close()
	})

	var result struct {
		Version string `json:"version"`
	}
	require.NoError(t, c.Do(ctx, `{version}`, "", nil, &result), "query version")

	select {
	case <-exporter.started:
	case <-time.After(30 * time.Second):
		t.Fatal("engine did not export telemetry spans")
	}

	// Close while the exporter is still blocked mid-export: it must not
	// return, and teardown must not start, until the export is released.
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- c.Close()
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while telemetry export was still blocked: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	exporter.releaseOnce.Do(func() { close(exporter.release) })

	select {
	case err := <-closeDone:
		require.NoError(t, err, "Close failed after telemetry export was released")
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not finish after telemetry export was released")
	}
	require.False(t, exporter.sawTeardown.Load(), "client teardown started before telemetry drained")
}

// blockingSpanExporter blocks each export until released, and records whether
// the client's teardown (closeCtx) started while it was still blocked.
type blockingSpanExporter struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once

	closeCtx    atomic.Pointer[context.Context] // set after Connect; spans flow during Connect too
	sawTeardown atomic.Bool
}

func (e *blockingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	e.startOnce.Do(func() { close(e.started) })

	teardown := context.Background().Done()
	if c := e.closeCtx.Load(); c != nil {
		teardown = (*c).Done()
	}

	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-teardown:
		e.sawTeardown.Store(true)
		return errors.New("client teardown started while telemetry export was blocked")
	}
}

func (e *blockingSpanExporter) Shutdown(context.Context) error {
	e.releaseOnce.Do(func() { close(e.release) })
	return nil
}
