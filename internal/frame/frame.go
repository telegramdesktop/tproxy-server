package frame

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

const (
	HeaderSize          = 8
	MaxPayload          = 1024 * 1024
	InitialStreamWindow = 4 * 1024 * 1024
	DataChunk           = 64 * 1024
	MaxStreamID         = 0xFFFFFF
	MaxBatchFrames      = 4096
)

type Type byte

const (
	Open    Type = 0x01
	Data    Type = 0x02
	Close   Type = 0x03
	Window  Type = 0x04
	Ping    Type = 0x05
	Pong    Type = 0x06
	OpenTCP Type = 0x07
	Hello   Type = 0x10
	Welcome Type = 0x11
	Bye     Type = 0x1F
)

type Frame struct {
	Type     Type
	StreamID uint32
	Payload  []byte
}

var (
	ErrIncomplete = errors.New("incomplete frame")
	ErrPayload    = errors.New("frame payload exceeds limit")
)

func Encode(t Type, streamID uint32, payload []byte) []byte {
	if streamID > MaxStreamID {
		panic("stream id exceeds 24 bits")
	}
	result := make([]byte, HeaderSize+len(payload))
	result[0] = byte(t)
	result[1] = byte(streamID >> 16)
	result[2] = byte(streamID >> 8)
	result[3] = byte(streamID)
	binary.BigEndian.PutUint32(result[4:8], uint32(len(payload)))
	copy(result[8:], payload)
	return result
}

func ParseAll(input []byte, maxPayload int) ([]Frame, error) {
	if maxPayload <= 0 || maxPayload > MaxPayload {
		maxPayload = MaxPayload
	}
	frames := make([]Frame, 0, 4)
	for len(input) != 0 {
		if len(frames) == MaxBatchFrames {
			return nil, errors.New("frame batch contains too many frames")
		}
		if len(input) < HeaderSize {
			return nil, ErrIncomplete
		}
		length := uint64(binary.BigEndian.Uint32(input[4:8]))
		if length > uint64(maxPayload) {
			return nil, ErrPayload
		}
		full := uint64(HeaderSize) + length
		if full > uint64(len(input)) {
			return nil, ErrIncomplete
		}
		streamID := uint32(input[1])<<16 | uint32(input[2])<<8 | uint32(input[3])
		frames = append(frames, Frame{
			Type:     Type(input[0]),
			StreamID: streamID,
			Payload:  input[HeaderSize:int(full)],
		})
		input = input[int(full):]
	}
	if len(frames) == 0 {
		return nil, errors.New("empty frame batch")
	}
	return frames, nil
}

func ParseHello(input []byte) error {
	frames, err := ParseAll(input, MaxPayload)
	if err != nil {
		return err
	}
	if len(frames) != 1 {
		return errors.New("HELLO must be the only frame")
	}
	value := frames[0]
	if value.Type != Hello || value.StreamID != 0 || len(value.Payload) != 1 || value.Payload[0] != 1 {
		return errors.New("invalid HELLO frame")
	}
	return nil
}

func WindowAmount(payload []byte) (uint32, error) {
	if len(payload) != 4 {
		return 0, errors.New("WINDOW payload must be four bytes")
	}
	value := binary.BigEndian.Uint32(payload)
	if value == 0 {
		return 0, errors.New("WINDOW delta must be nonzero")
	}
	return value, nil
}

func WindowPayload(amount uint32) []byte {
	result := make([]byte, 4)
	binary.BigEndian.PutUint32(result, amount)
	return result
}

func EncodeOpenTCPPayload(target netip.AddrPort) ([]byte, error) {
	if !target.IsValid() || target.Port() == 0 {
		return nil, errors.New("OPEN_TCP target requires a valid IP address and nonzero port")
	}
	addr := target.Addr().Unmap()
	if addr.Is4() {
		raw := addr.As4()
		result := make([]byte, 7)
		result[0] = 4
		copy(result[1:5], raw[:])
		binary.BigEndian.PutUint16(result[5:7], target.Port())
		return result, nil
	}
	raw := addr.As16()
	result := make([]byte, 19)
	result[0] = 16
	copy(result[1:17], raw[:])
	binary.BigEndian.PutUint16(result[17:19], target.Port())
	return result, nil
}

func DecodeOpenTCPTarget(payload []byte) (netip.AddrPort, error) {
	if len(payload) == 0 {
		return netip.AddrPort{}, errors.New("OPEN_TCP payload must not be empty")
	}
	switch payload[0] {
	case 4:
		if len(payload) != 7 {
			return netip.AddrPort{}, errors.New("OPEN_TCP IPv4 payload must be 7 bytes")
		}
		var raw [4]byte
		copy(raw[:], payload[1:5])
		port := binary.BigEndian.Uint16(payload[5:7])
		if port == 0 {
			return netip.AddrPort{}, errors.New("OPEN_TCP port must be nonzero")
		}
		return netip.AddrPortFrom(netip.AddrFrom4(raw), port), nil
	case 16:
		if len(payload) != 19 {
			return netip.AddrPort{}, errors.New("OPEN_TCP IPv6 payload must be 19 bytes")
		}
		var raw [16]byte
		copy(raw[:], payload[1:17])
		port := binary.BigEndian.Uint16(payload[17:19])
		if port == 0 {
			return netip.AddrPort{}, errors.New("OPEN_TCP port must be nonzero")
		}
		return netip.AddrPortFrom(netip.AddrFrom16(raw), port), nil
	default:
		return netip.AddrPort{}, fmt.Errorf("OPEN_TCP address length %d is invalid", payload[0])
	}
}

func ValidateClientShape(value Frame) error {
	if value.StreamID == 0 {
		if value.Type != Pong || len(value.Payload) > 64 {
			return fmt.Errorf("frame type %#x is invalid on stream zero", value.Type)
		}
		return nil
	}
	switch value.Type {
	case Open, Close:
		if len(value.Payload) != 0 {
			return fmt.Errorf("frame type %#x requires an empty payload", value.Type)
		}
	case OpenTCP:
		if _, err := DecodeOpenTCPTarget(value.Payload); err != nil {
			return err
		}
	case Data:
		if len(value.Payload) == 0 {
			return errors.New("DATA payload must be nonempty")
		}
	case Window:
		if _, err := WindowAmount(value.Payload); err != nil {
			return err
		}
	default:
		return fmt.Errorf("frame type %#x is invalid client-to-relay", value.Type)
	}
	return nil
}
