package gateway

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestHubRegisterAndUnregister(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go h.Run()

	c := &Client{send: make(chan []byte, 1)}
	h.register <- c

	deadline := time.After(500 * time.Millisecond)
	for h.ClientCount() != 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for client registration")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	h.unregister <- c
	deadline = time.After(500 * time.Millisecond)
	for h.ClientCount() != 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for client unregistration")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestHubBroadcast(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go h.Run()

	c := &Client{send: make(chan []byte, 1)}
	h.register <- c

	deadline := time.After(500 * time.Millisecond)
	for h.ClientCount() != 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for client registration")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	want := []byte("hello")
	h.Broadcast(want)

	select {
	case got := <-c.send:
		if string(got) != string(want) {
			t.Fatalf("broadcast payload mismatch: got %q, want %q", got, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast payload")
	}
}
