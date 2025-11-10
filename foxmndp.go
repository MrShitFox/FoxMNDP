package FoxMNDP

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// MNDP TLV (Type-Length-Value) attribute types.
const (
	tlvMACAddress uint16 = 1
	tlvIdentity   uint16 = 5
	tlvVersion    uint16 = 7
	tlvPlatform   uint16 = 8
	tlvUptime     uint16 = 10
	tlvBoard      uint16 = 12
)

// Device represents a discovered Mikrotik device.
type Device struct {
	IPAddress  string         // IP address of the device
	MACAddress net.HardwareAddr // MAC address of the device
	Identity   string         // Configured device identity
	Version    string         // RouterOS version
	Platform   string         // Device platform (e.g., "MikroTik")
	Uptime     time.Duration  // Device uptime
	Board      string         // Hardware board model (e.g., "RB4011iGS+")
}

// Options holds configuration for the discovery service.
type Options struct {
	Port    int    // UDP port to listen on. Default: 5678.
	Host    string // Host IP address to bind to. Default: "0.0.0.0".
	Version string // Network protocol. "udp4" or "udp6". Default: "udp4".
}

// FoxMNDP is the main discovery service client.
type FoxMNDP struct {
	options Options
	conn    net.PacketConn

	// Channels for event communication
	DeviceFound chan Device
	Error       chan error
	Started     chan string
	Stopped     chan struct{}

	stopChan chan struct{} // Internal signal channel for stopping
}

// New creates a new FoxMNDP service instance.
func New(options Options) (*FoxMNDP, error) {
	// Apply default values
	if options.Port == 0 {
		options.Port = 5678
	}
	if options.Host == "" {
		options.Host = "0.0.0.0"
	}
	if options.Version == "" {
		options.Version = "udp4"
	}

	// Use "::" for IPv6 "any" address
	if options.Version == "udp6" && options.Host == "0.0.0.0" {
		options.Host = "::"
	}

	return &FoxMNDP{
		options:     options,
		DeviceFound: make(chan Device, 10), // Buffered channels to avoid blocking
		Error:       make(chan error, 5),
		Started:     make(chan string, 1),
		Stopped:     make(chan struct{}, 1),
		stopChan:    make(chan struct{}),
	}, nil
}

// Start begins listening for MNDP packets in a new goroutine.
func (f *FoxMNDP) Start() {
	addr := net.JoinHostPort(f.options.Host, strconv.Itoa(f.options.Port))
	conn, err := net.ListenPacket(f.options.Version, addr)
	if err != nil {
		// Send a fatal error if we can't bind
		f.Error <- fmt.Errorf("failed to bind to %s: %w", addr, err)
		return
	}
	f.conn = conn

	f.Started <- fmt.Sprintf("FoxMNDP listener started on %s", conn.LocalAddr().String())

	go f.listen()
}

// Stop gracefully shuts down the discovery service.
func (f *FoxMNDP) Stop() {
	// Ensure stop is idempotent
	select {
	case <-f.stopChan:
		// Already stopping or stopped
		return
	default:
		close(f.stopChan)
		if f.conn != nil {
			f.conn.Close() // This will unblock the ReadFrom call in listen()
		}
		f.Stopped <- struct{}{}
		
		// Clean up channels
		close(f.DeviceFound)
		close(f.Error)
		close(f.Started)
		close(f.Stopped)
	}
}

// listen is the main loop that reads packets from the connection.
func (f *FoxMNDP) listen() {
	buf := make([]byte, 1500) // Standard MTU size
	for {
		n, rinfo, err := f.conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-f.stopChan:
				// Expected error on Stop()
				return
			default:
				// Unexpected network error
				f.Error <- fmt.Errorf("failed to read from socket: %w", err)
				if errors.Is(err, net.ErrClosed) {
					return // Exit loop if connection is closed
				}
				continue // Try to recover
			}
		}

		// Copy the buffer to avoid data race
		packet := make([]byte, n)
		copy(packet, buf[:n])

		// Parse the packet in a new goroutine to avoid
		// blocking the listener loop.
		go f.parsePacket(packet, rinfo)
	}
}

// parsePacket decodes a raw MNDP packet and sends the result.
func (f *FoxMNDP) parsePacket(buffer []byte, rinfo net.Addr) {
	// Recover from panics during parsing (e.g., malformed packet)
	defer func() {
		if r := recover(); r != nil {
			f.Error <- fmt.Errorf("panic while parsing packet: %v", r)
		}
	}()

	// MNDP TLV data starts after a 4-byte header.
	if len(buffer) < 8 { // Must have at least header + 1 TLV header
		return // Too short, ignore.
	}
	
	reader := bytes.NewReader(buffer[4:]) // Skip the 4-byte header

	ipAddr, _, _ := net.SplitHostPort(rinfo.String())
	device := Device{
		IPAddress: ipAddr,
	}

	// Read TLV (Type-Length-Value) attributes
	for reader.Len() >= 4 { // Must have at least Type (2) + Length (2)
		var tlvType, tlvLength uint16

		// Read Type (Big Endian)
		if err := binary.Read(reader, binary.BigEndian, &tlvType); err != nil {
			f.Error <- fmt.Errorf("failed to read TLV type: %w", err)
			return
		}

		// Read Length (Big Endian)
		if err := binary.Read(reader, binary.BigEndian, &tlvLength); err != nil {
			f.Error <- fmt.Errorf("failed to read TLV length: %w", err)
			return
		}

		// Check for corrupt packet
		if reader.Len() < int(tlvLength) {
			f.Error <- fmt.Errorf("corrupt packet: expected length %d, have %d", tlvLength, reader.Len())
			return
		}

		// Read Value
		value := make([]byte, tlvLength)
		if _, err := reader.Read(value); err != nil {
			f.Error <- fmt.Errorf("failed to read TLV value: %w", err)
			return
		}

		// Assign value based on type
		switch tlvType {
		case tlvMACAddress:
			device.MACAddress = net.HardwareAddr(value)

		case tlvIdentity:
			device.Identity = string(value)

		case tlvVersion:
			device.Version = string(value)

		case tlvPlatform:
			device.Platform = string(value)

		case tlvBoard:
			device.Board = string(value)

		case tlvUptime:
			if len(value) == 4 {
				// Uptime is a 4-byte Little Endian integer
				uptimeSeconds := binary.LittleEndian.Uint32(value)
				device.Uptime = time.Duration(uptimeSeconds) * time.Second
			}
		}
	}

	// Send the fully populated device struct
	f.DeviceFound <- device
}
