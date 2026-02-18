package client

import (
	"fmt"

	"github.com/lajosnagyuk/prisn/pkg/log"
)

// OfflineResult wraps a result with offline status information.
type OfflineResult[T any] struct {
	// Data is the result data (from server or cache)
	Data T

	// Offline indicates whether the data came from cache due to server being unreachable
	Offline bool

	// Message provides additional context when offline
	Message string
}

// WithOfflineFallback executes an operation and handles offline scenarios.
// If the server is unreachable and a fallback is provided, it returns the fallback
// with a warning. Otherwise, it returns the error.
func WithOfflineFallback[T any](operation func() (T, error), fallback *T, fallbackMsg string) (*OfflineResult[T], error) {
	result, err := operation()
	if err == nil {
		return &OfflineResult[T]{
			Data:    result,
			Offline: false,
		}, nil
	}

	// Check if this is a connection error
	if !IsOffline(err) {
		var zero T
		return &OfflineResult[T]{Data: zero}, err
	}

	// Server is offline
	if fallback != nil {
		log.Warn("Server unreachable. Showing local data.")
		if fallbackMsg != "" {
			log.Info("  %s", fallbackMsg)
		}
		fmt.Println()

		return &OfflineResult[T]{
			Data:    *fallback,
			Offline: true,
			Message: "Server unreachable - showing local data",
		}, nil
	}

	// No fallback available
	var zero T
	return &OfflineResult[T]{Data: zero}, fmt.Errorf("server unreachable and no local data available: %w", err)
}

// HandleOfflineError provides user-friendly messaging for offline errors.
// Returns true if the error was an offline error and was handled.
func HandleOfflineError(err error) bool {
	if !IsOffline(err) {
		return false
	}

	connErr, _ := err.(*ConnectError)
	log.Fail("Cannot connect to server: %s", connErr.Server)
	fmt.Println()
	fmt.Println("Possible causes:")
	fmt.Println("  - Server is not running")
	fmt.Println("  - Wrong server address")
	fmt.Println("  - Network/firewall issue")
	fmt.Println()
	fmt.Println("Check your context with: prisn context current -v")
	fmt.Println("Start local server with: prisn server")

	return true
}

// RequireOnline returns an error if the server is offline.
// Use this for operations that cannot work offline (deploy, scale, etc.)
func RequireOnline(c *Client) error {
	// Try a simple health check
	resp, err := c.Get("/health")
	if err != nil {
		if IsOffline(err) {
			return fmt.Errorf("this operation requires server connection\n\nServer %s is unreachable.\nCheck your context: prisn context current -v", c.ctx.Server)
		}
		return err
	}
	resp.Body.Close()
	return nil
}
