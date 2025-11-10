# FoxMNDP

A simple, zero-dependency Go library for discovering Mikrotik devices on your network using the MNDP (Mikrotik Neighbor Discovery Protocol).

## 💡 About

FoxMNDP listens on the standard MNDP port (`UDP 5678`) for broadcast packets sent by Mikrotik devices. It parses these packets and reports any discovered device through a simple, channel-based Go API.

This library is designed to be:

  * **Simple:** Just initialize, start, and read from a channel.
  * **Reliable:** Built for production use with graceful start/stop.
  * **Self-Contained:** Has **zero** external dependencies.

## 💾 Installation

To use FoxMNDP in your project, you can get it with:

```bash
go get github.com/MrShitFox/FoxMNDP
```

## 🚀 Usage

Here is a complete example of how to use the library to listen for devices and shut down gracefully on `Ctrl+C`.

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	// Import the library
	"github.com/MrShitFox/FoxMNDP"
)

func main() {
	// 1. Initialize with default options
	// (You can also specify port/host/version in FoxMNDP.Options{})
	discovery, err := FoxMNDP.New(FoxMNDP.Options{})
	if err != nil {
		log.Fatalf("Failed to create service: %v", err)
	}

	// Channel for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// 2. Start a goroutine to handle all events
	go func() {
		for {
			select {
			// A device was found
			case device, ok := <-discovery.DeviceFound:
				if !ok { return } // Channel closed
				log.Printf("\n=== DEVICE FOUND ===\n")
				log.Printf("  IP:       %s\n", device.IPAddress)
				log.Printf("  MAC:      %s\n", device.MACAddress)
				log.Printf("  Identity: %s\n", device.Identity)
				log.Printf("  Board:    %s\n", device.Board)
				log.Printf("====================\n")

			// An error occurred (e.g., failed to bind)
			case err, ok := <-discovery.Error:
				if !ok { return }
				log.Printf("[ERROR]: %v", err)

			// The listener has successfully started
			case msg, ok := <-discovery.Started:
				if !ok { return }
				log.Println(msg)

			// The listener has stopped
			case <-discovery.Stopped:
				log.Println("Listener stopped.")
				return
			}
		}
	}()

	// 3. Start the listener
	// This will trigger the 'Started' event
	discovery.Start()

	log.Println("Listening for Mikrotik devices... Press Ctrl+C to exit.")

	// 4. Wait for shutdown signal
	<-quit
	log.Println("Shutting down...")

	// 5. Stop the listener
	// This will trigger the 'Stopped' event
	discovery.Stop()
}
```

### The `Device` Struct

When a device is found, you will receive a `FoxMNDP.Device` struct with the following fields:

```go
type Device struct {
    IPAddress  string         // IP address of the device
    MACAddress net.HardwareAddr // MAC address of the device
    Identity   string         // Configured device identity
    Version    string         // RouterOS version
    Platform   string         // Device platform (e.g., "MikroTik")
    Uptime     time.Duration  // Device uptime
    Board      string         // Hardware board model (e.g., "RB4011iGS+")
}
```

## ⚖️ License

This project is licensed under the **GPL v3 License**.
