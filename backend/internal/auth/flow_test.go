package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func testSealer(t *testing.T, secret byte) *FlowSealer {
	t.Helper()
	s, err := NewFlowSealer(bytes.Repeat([]byte{secret}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFlowSealRoundTrip(t *testing.T) {
	s := testSealer(t, 1)
	st := NewFlowState("/calendar?week=45", time.Now())
	v, err := s.Seal(st)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v, st.Verifier) || strings.Contains(v, "calendar") {
		t.Error("cookie value must be encrypted")
	}
	got, err := s.Open(v)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != st.State || got.Nonce != st.Nonce || got.Verifier != st.Verifier || got.ReturnTo != "/calendar?week=45" {
		t.Errorf("round trip mismatch: %+v vs %+v", got, st)
	}
}

func TestFlowOpenRejects(t *testing.T) {
	s := testSealer(t, 1)
	v, _ := s.Seal(NewFlowState("/", time.Now()))

	// Flip one character in the middle: GCM authentication must fail.
	b := []byte(v)
	if b[20] == 'A' {
		b[20] = 'B'
	} else {
		b[20] = 'A'
	}
	tampered := string(b)

	expired := testSealer(t, 1)
	expired.now = func() time.Time { return time.Now().Add(FlowTTL + time.Second) }

	cases := map[string]func() error{
		"tampered":  func() error { _, err := s.Open(tampered); return err },
		"other key": func() error { _, err := testSealer(t, 2).Open(v); return err },
		"expired":   func() error { _, err := expired.Open(v); return err },
		"garbage":   func() error { _, err := s.Open("not base64 !!"); return err },
		"empty":     func() error { _, err := s.Open(""); return err },
		"too short": func() error { _, err := s.Open("AAAA"); return err },
	}
	for name, open := range cases {
		if err := open(); err != ErrInvalidFlow {
			t.Errorf("%s: err = %v, want ErrInvalidFlow", name, err)
		}
	}
}

func TestNewFlowSealerRequiresLongSecret(t *testing.T) {
	if _, err := NewFlowSealer(make([]byte, 16)); err == nil {
		t.Error("want error for 16-byte secret")
	}
}

func TestNewFlowStateIsRandom(t *testing.T) {
	a, b := NewFlowState("/", time.Now()), NewFlowState("/", time.Now())
	if a.State == b.State || a.Nonce == b.Nonce || a.Verifier == b.Verifier || a.State == a.Nonce {
		t.Error("state, nonce and verifier must be fresh random values")
	}
	if len(a.Verifier) < 43 || len(a.Verifier) > 128 {
		t.Errorf("PKCE verifier length %d outside 43..128", len(a.Verifier))
	}
}

func TestSafeReturnTo(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/":                      "/",
		"/calendar":              "/calendar",
		"/events/1?x=y":          "/events/1?x=y",
		"https://evil.example":   "/",
		"//evil.example":         "/",
		`/\evil.example`:         "/",
		"evil.example":           "/",
		"javascript:alert(1)":    "/",
		"/ok\r\nSet-Cookie: x=1": "/",
	}
	for in, want := range cases {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}
