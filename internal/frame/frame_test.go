package frame

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	input := append(Encode(Open, MaxStreamID, nil), Encode(Data, 7, []byte("payload"))...)
	frames, err := ParseAll(input, MaxPayload)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0].StreamID != MaxStreamID || !bytes.Equal(frames[1].Payload, []byte("payload")) {
		t.Fatalf("unexpected frames: %#v", frames)
	}
}

func TestRejectsPartialAndOversized(t *testing.T) {
	if _, err := ParseAll(Encode(Data, 1, []byte("x"))[:8], MaxPayload); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected incomplete frame, got %v", err)
	}
	if _, err := ParseAll(Encode(Data, 1, []byte("xx")), 1); !errors.Is(err, ErrPayload) {
		t.Fatalf("expected oversized payload, got %v", err)
	}
}

func TestHelloAndWindow(t *testing.T) {
	if err := ParseHello(Encode(Hello, 0, []byte{1})); err != nil {
		t.Fatal(err)
	}
	if err := ParseHello(Encode(Hello, 0, []byte{2})); err == nil {
		t.Fatal("accepted unsupported protocol version")
	}
	if _, err := WindowAmount([]byte{0, 0, 0, 0}); err == nil {
		t.Fatal("accepted zero window")
	}
}

func TestRejectsExcessiveFrameCount(t *testing.T) {
	input := bytes.Repeat(Encode(Close, 1, nil), MaxBatchFrames+1)
	if _, err := ParseAll(input, MaxPayload); err == nil {
		t.Fatal("accepted excessive frame count")
	}
}

func TestOpenTCPTargetCodecAndShape(t *testing.T) {
	v4 := netip.MustParseAddrPort("149.154.167.99:443")
	v4Payload, err := EncodeOpenTCPPayload(v4)
	if err != nil || len(v4Payload) != 7 {
		t.Fatalf("EncodeOpenTCPPayload IPv4 failed: len=%d err=%v", len(v4Payload), err)
	}
	decodedV4, err := DecodeOpenTCPTarget(v4Payload)
	if err != nil || decodedV4 != v4 {
		t.Fatalf("DecodeOpenTCPTarget IPv4 got %v (%v), want %v", decodedV4, err, v4)
	}
	if err := ValidateClientShape(Frame{Type: OpenTCP, StreamID: 1, Payload: v4Payload}); err != nil {
		t.Fatalf("ValidateClientShape rejected valid IPv4 OpenTCP: %v", err)
	}

	v6 := netip.MustParseAddrPort("[2001:67c:4e8:f002::a]:5222")
	v6Payload, err := EncodeOpenTCPPayload(v6)
	if err != nil || len(v6Payload) != 19 {
		t.Fatalf("EncodeOpenTCPPayload IPv6 failed: len=%d err=%v", len(v6Payload), err)
	}
	decodedV6, err := DecodeOpenTCPTarget(v6Payload)
	if err != nil || decodedV6 != v6 {
		t.Fatalf("DecodeOpenTCPTarget IPv6 got %v (%v), want %v", decodedV6, err, v6)
	}
	if err := ValidateClientShape(Frame{Type: OpenTCP, StreamID: 3, Payload: v6Payload}); err != nil {
		t.Fatalf("ValidateClientShape rejected valid IPv6 OpenTCP: %v", err)
	}

	if err := ValidateClientShape(Frame{Type: OpenTCP, StreamID: 0, Payload: v4Payload}); err == nil {
		t.Fatal("accepted OpenTCP on stream zero")
	}
	for _, bad := range [][]byte{
		nil,
		{4, 149, 154, 167, 99, 0, 0},                        // port 0
		{4, 149, 154, 167, 99, 1},                           // truncated IPv4
		{4, 149, 154, 167, 99, 1, 0xbb, 0},                  // oversized IPv4
		{6, 149, 154, 167, 99, 1, 0xbb},                     // invalid addr_len
		{16, 0x20, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0xbb},   // truncated IPv6
		append(append([]byte{16}, make([]byte, 16)...), 0, 0), // IPv6 port 0
	} {
		if err := ValidateClientShape(Frame{Type: OpenTCP, StreamID: 1, Payload: bad}); err == nil {
			t.Fatalf("accepted invalid OpenTCP payload %x", bad)
		}
	}
}
