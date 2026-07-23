package api

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServer_ShutdownBeforeServeAvoidsRace proves the eager-construction fix
// (Prepare called synchronously before any goroutine that could race with a
// shutdown signal) actually closes the race described in Task 20: a Shutdown
// call arriving essentially immediately after startup — before the Serve
// goroutine has even been scheduled — must not panic and must not be lost.
// It should instead be observed by Serve once it does run, causing Serve to
// return promptly (http.ErrServerClosed, mapped to nil) rather than binding
// the port and blocking on Accept forever.
//
// Before the fix (lazily constructing *http.Server inside the goroutine that
// calls Serve/ListenAndServe), the equivalent of this Shutdown call would
// either race a nil field (panic) or silently no-op because the server
// object didn't exist yet.
func TestServer_ShutdownBeforeServeAvoidsRace(t *testing.T) {
	srv := newTestServer(t)

	// Mirror runServer/runAll: construct the *http.Server synchronously,
	// before any goroutine exists that could touch it.
	srv.Prepare()
	if srv.httpServer == nil {
		t.Fatal("expected Prepare to construct a non-nil *http.Server")
	}
	// Use an ephemeral port so this test can't collide with a real instance.
	srv.httpServer.Addr = "127.0.0.1:0"

	// A stop signal arrives before the Serve goroutine has been scheduled at
	// all.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown before Serve started returned error: %v", err)
	}

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve()
	}()

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("expected Serve to return nil after a Shutdown that preceded it, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return promptly after a Shutdown that preceded it — the earlier Shutdown was lost")
	}
}

// TestServer_ShutdownWithoutPrepareIsNoop proves Shutdown is safe (does not
// panic) even if Prepare/Serve/Run were never called, e.g. a code path that
// calls Shutdown defensively without knowing whether the server ever started.
func TestServer_ShutdownWithoutPrepareIsNoop(t *testing.T) {
	srv := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("expected Shutdown with no prior Prepare to be a no-op, got error: %v", err)
	}
}

// TestServer_ShutdownWaitsForInFlightRequest proves Shutdown genuinely lets
// an in-flight request finish before returning, rather than cutting it off —
// the core promise of graceful shutdown. A deliberately short, test-only
// handler is wired directly onto the prepared *http.Server (bypassing the
// production route table) so the test can control exactly when the handler
// completes without depending on any production endpoint's timing.
func TestServer_ShutdownWaitsForInFlightRequest(t *testing.T) {
	srv := newTestServer(t)

	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	handlerCompleted := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-releaseHandler
		w.WriteHeader(http.StatusOK)
		close(handlerCompleted)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	srv.httpServer = &http.Server{Handler: mux}

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.httpServer.Serve(ln)
	}()

	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err == nil {
			resp.Body.Close()
		}
		reqDone <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow handler never started")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- srv.Shutdown(ctx)
	}()

	// Shutdown must still be waiting on the in-flight request; it must not
	// have returned (or the handler completed) yet.
	select {
	case <-handlerCompleted:
		t.Fatal("handler completed before the test released it — test setup is broken")
	case <-shutdownDone:
		t.Fatal("Shutdown returned before the in-flight request completed")
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseHandler)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return after the in-flight request completed")
	}

	select {
	case err := <-reqDone:
		if err != nil {
			t.Fatalf("in-flight request failed instead of completing: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected the in-flight request to have completed by the time Shutdown returned")
	}

	<-serveDone
}
