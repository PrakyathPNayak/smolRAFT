// Package gateway implements the WebSocket hub and HTTP handlers
// for the smolRAFT collaborative drawing board gateway.
package gateway

import (
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
)

// Client represents a connected WebSocket client.
type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub manages all connected WebSocket clients and broadcasts
// committed strokes to all of them.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	logger     *slog.Logger
}

// NewHub creates a new Hub.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		logger:     logger.With("component", "hub"),
	}
}

// Run is the main event loop for the hub. It handles client registration,
// unregistration, and message broadcasting. Blocks until the broadcast
// channel is closed.
func (h *Hub) Run() {
	for {
		select {
		case client, ok := <-h.register:
			if !ok {
				return
			}
			h.mu.Lock()
			h.clients[client] = true
			count := len(h.clients)
			h.mu.Unlock()
			h.logger.Info("client connected", "total", count)

		case client, ok := <-h.unregister:
			if !ok {
				return
			}
			h.mu.Lock()
			if _, exists := h.clients[client]; exists {
				delete(h.clients, client)
				close(client.send)
			}
			count := len(h.clients)
			h.mu.Unlock()
			h.logger.Info("client disconnected", "total", count)

		case message, ok := <-h.broadcast:
			if !ok {
				return
			}
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client too slow — drop it
					go func(c *Client) {
						h.unregister <- c
					}(client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(data []byte) {
	select {
	case h.broadcast <- data:
	default:
		h.logger.Warn("broadcast channel full, dropping message")
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
