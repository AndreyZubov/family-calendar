package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	if err == nil {
		return nil
	}
	ve, ok := IsValidation(err)
	if !ok {
		t.Fatalf("want *ValidationError, got %T: %v", err, err)
	}
	return ve.Fields
}

func TestEventInputNormalize(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	tests := []struct {
		name      string
		in        EventInput
		wantField string // "" = valid
	}{
		{"valid", EventInput{Title: "Dinner", StartsAt: t0, EndsAt: t0.Add(time.Hour)}, ""},
		{"zero-length event", EventInput{Title: "Call", StartsAt: t0, EndsAt: t0}, ""},
		{"empty title", EventInput{Title: "   ", StartsAt: t0, EndsAt: t0}, "title"},
		{"title 200 runes ok", EventInput{Title: strings.Repeat("я", 200), StartsAt: t0, EndsAt: t0}, ""},
		{"title 201 runes", EventInput{Title: strings.Repeat("я", 201), StartsAt: t0, EndsAt: t0}, "title"},
		{"long description", EventInput{Title: "x", Description: strings.Repeat("a", 5001), StartsAt: t0, EndsAt: t0}, "description"},
		{"missing start", EventInput{Title: "x", EndsAt: t0}, "startsAt"},
		{"missing end", EventInput{Title: "x", StartsAt: t0}, "endsAt"},
		{"ends before start", EventInput{Title: "x", StartsAt: t0, EndsAt: t0.Add(-time.Minute)}, "endsAt"},
		{"too long", EventInput{Title: "x", StartsAt: t0, EndsAt: t0.Add(367 * 24 * time.Hour)}, "endsAt"},
		{"too many participants", EventInput{Title: "x", StartsAt: t0, EndsAt: t0, ParticipantIDs: manyIDs(51)}, "participantIds"},
		{"51 unique after dedupe", EventInput{Title: "x", StartsAt: t0, EndsAt: t0, ParticipantIDs: append(manyIDs(50), a, a, b)[:52]}, "participantIds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			fields := fieldsOf(t, in.normalize())
			if tt.wantField == "" && fields != nil {
				t.Errorf("unexpected errors: %v", fields)
			}
			if tt.wantField != "" && fields[tt.wantField] == "" {
				t.Errorf("want error on %q, got %v", tt.wantField, fields)
			}
		})
	}
}

func manyIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}
	return ids
}

func TestNormalizeCleansInput(t *testing.T) {
	p := uuid.New()
	moscow := time.FixedZone("MSK", 3*3600)
	in := EventInput{
		Title: "  Dinner  ", Description: "\n notes \n",
		StartsAt: t0.In(moscow), EndsAt: t0.Add(time.Hour).In(moscow),
		ParticipantIDs: []uuid.UUID{p, p, p},
	}
	if err := in.normalize(); err != nil {
		t.Fatal(err)
	}
	if in.Title != "Dinner" || in.Description != "notes" {
		t.Errorf("not trimmed: %q %q", in.Title, in.Description)
	}
	if in.StartsAt.Location() != time.UTC || !in.StartsAt.Equal(t0) {
		t.Errorf("not converted to UTC: %v", in.StartsAt)
	}
	if len(in.ParticipantIDs) != 1 {
		t.Errorf("duplicates kept: %v", in.ParticipantIDs)
	}
}

func TestNormalizeKeepsNilParticipants(t *testing.T) {
	// nil means "keep current participants" on update; it must stay nil.
	in := EventInput{Title: "x", StartsAt: t0, EndsAt: t0}
	_ = in.normalize()
	if in.ParticipantIDs != nil {
		t.Error("nil ParticipantIDs became non-nil")
	}
	in.ParticipantIDs = []uuid.UUID{}
	_ = in.normalize()
	if in.ParticipantIDs == nil {
		t.Error("empty ParticipantIDs became nil (would mean 'keep' instead of 'remove all')")
	}
}

func TestValidateRange(t *testing.T) {
	cases := map[string]struct {
		from, to time.Time
		field    string
	}{
		"ok":           {t0, t0.Add(31 * 24 * time.Hour), ""},
		"missing from": {time.Time{}, t0, "from"},
		"missing to":   {t0, time.Time{}, "to"},
		"empty range":  {t0, t0, "to"},
		"reversed":     {t0, t0.Add(-time.Hour), "to"},
		"too wide":     {t0, t0.Add(401 * 24 * time.Hour), "to"},
	}
	for name, c := range cases {
		fields := fieldsOf(t, validateRange(c.from, c.to))
		if (c.field == "") != (fields == nil) || (c.field != "" && fields[c.field] == "") {
			t.Errorf("%s: fields=%v, want error on %q", name, fields, c.field)
		}
	}
}

func TestValidateFamilyName(t *testing.T) {
	if name, err := validateFamilyName("  Ивановы "); err != nil || name != "Ивановы" {
		t.Errorf("got %q, %v", name, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 101)} {
		if _, err := validateFamilyName(bad); fieldsOf(t, err)["name"] == "" {
			t.Errorf("%q: want name error", bad)
		}
	}
}

func TestValidationErrorMessageIsStable(t *testing.T) {
	err := &ValidationError{Fields: map[string]string{"title": "required", "endsAt": "bad"}}
	if got := err.Error(); got != "invalid input: endsAt: bad; title: required" {
		t.Errorf("Error() = %q", got)
	}
}
