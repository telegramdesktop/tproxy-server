package frame

import (
	"bytes"
	"errors"
	"math"
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

func TestComputePadPayloadsExhaustive(t *testing.T) {
	if err := ValidateClientShape(Frame{Type: Pad, StreamID: 0, Payload: nil}); err != nil {
		t.Fatalf("rejected empty Pad frame on stream 0: %v", err)
	}
	if err := ValidateClientShape(Frame{Type: Pad, StreamID: 0, Payload: make([]byte, MaxPadPayload)}); err != nil {
		t.Fatalf("rejected 255-byte Pad frame on stream 0: %v", err)
	}
	if err := ValidateClientShape(Frame{Type: Pad, StreamID: 1, Payload: nil}); err == nil {
		t.Fatal("accepted Pad frame on nonzero stream")
	}
	if err := ValidateClientShape(Frame{Type: Pad, StreamID: 0, Payload: make([]byte, MaxPadPayload+1)}); err == nil {
		t.Fatal("accepted oversized Pad frame on stream 0")
	}

	buckets := []int{64, 128, 256, 384, 512, 768, 1024}
	for _, targetBucket := range buckets {
		for rawLen := 1; rawLen <= 4096; rawLen++ {
			payloads := ComputePadPayloads(rawLen, targetBucket)
			if len(payloads) == 0 {
				t.Fatalf("ComputePadPayloads(%d, %d) returned empty payloads", rawLen, targetBucket)
			}
			paddedLen := rawLen
			for _, p := range payloads {
				if p < 0 || p > MaxPadPayload {
					t.Fatalf("ComputePadPayloads(%d, %d) produced out-of-range payload %d", rawLen, targetBucket, p)
				}
				paddedLen += HeaderSize + p
			}
			if paddedLen < rawLen+HeaderSize {
				t.Fatalf("ComputePadPayloads(%d, %d) added fewer than %d bytes: paddedLen=%d", rawLen, targetBucket, HeaderSize, paddedLen)
			}
			if rawLen < targetBucket {
				if paddedLen < targetBucket || (paddedLen-targetBucket)%PadBucketStep != 0 {
					t.Fatalf("ComputePadPayloads(%d, %d) misaligned paddedLen=%d", rawLen, targetBucket, paddedLen)
				}
			} else if paddedLen%PadBucketStep != 0 {
				t.Fatalf("ComputePadPayloads(%d, %d) above-bucket misaligned paddedLen=%d", rawLen, targetBucket, paddedLen)
			}
		}
	}
}

func TestCallPaddingStatisticalEntropy(t *testing.T) {
	defaultBuckets := []int{256, 384, 512, 768}
	sizeCounts := make(map[int]int)
	const samples = 500
	for i := 0; i < samples; i++ {
		rawPayloadLen := 52 + (i % 81) // raw frame length 60..140 bytes (typical 20ms Opus speech)
		rawBatch := Encode(Data, 7, bytes.Repeat([]byte{0x42}, rawPayloadLen))
		padded := AppendCallPadding(rawBatch, 2*1024*1024, defaultBuckets)
		if len(padded) <= len(rawBatch) {
			t.Fatalf("sample %d was not padded: raw=%d padded=%d", i, len(rawBatch), len(padded))
		}
		sizeCounts[len(padded)]++
		parsed, err := ParseAll(padded, MaxPayload)
		if err != nil {
			t.Fatalf("ParseAll failed on padded batch: %v", err)
		}
		stripped := StripPadFrames(parsed)
		if len(stripped) != 1 || stripped[0].Type != Data || stripped[0].StreamID != 7 || len(stripped[0].Payload) != rawPayloadLen {
			t.Fatalf("StripPadFrames corrupted batch: %#v", stripped)
		}
	}
	if len(sizeCounts) < 2 {
		t.Fatalf("expected at least 2 distinct padded bucket sizes, got %v", sizeCounts)
	}
	entropy := 0.0
	for _, count := range sizeCounts {
		p := float64(count) / float64(samples)
		entropy -= p * math.Log2(p)
	}
	if entropy < 0.8 {
		t.Fatalf("expected padded packet size Shannon entropy >= 0.8 bits, got %.3f bits (counts=%v)", entropy, sizeCounts)
	}
}

