package orders

import (
	"crypto/sha256"
	"encoding/json"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"kacheem/backend/internal/catalog"
)

// Input is what the buyer submits on the booking form.
type Input struct {
	Quantity int    `json:"quantity"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Age      int    `json:"age"`
}

// FieldErrors maps form fields to a short, displayable problem.
type FieldErrors map[string]string

func (f FieldErrors) Error() string { return "invalid booking details" }

var (
	phoneSeparators = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "")
	e164            = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

const (
	maxNameRunes = 100
	maxEmailLen  = 254
	maxAge       = 120
)

// Normalize validates in against the event's rules and returns the canonical
// form that is stored and digested. Every problem is reported at once.
func Normalize(e catalog.Event, in Input) (Input, error) {
	errs := FieldErrors{}
	out := Input{Quantity: in.Quantity, Age: in.Age}

	if in.Quantity < 1 || in.Quantity > e.MaxPerOrder {
		errs["quantity"] = "Choose between 1 and " + strconv.Itoa(e.MaxPerOrder) + " tickets."
	}

	out.Name = strings.Join(strings.Fields(in.Name), " ")
	switch {
	case out.Name == "":
		errs["name"] = "Enter the purchaser's name."
	case utf8.RuneCountInString(out.Name) > maxNameRunes:
		errs["name"] = "Use at most 100 characters."
	case strings.IndexFunc(out.Name, unicode.IsControl) >= 0:
		errs["name"] = "Remove unsupported characters."
	}

	out.Email = strings.TrimSpace(in.Email)
	if addr, err := mail.ParseAddress(out.Email); err != nil || addr.Address != out.Email ||
		len(out.Email) > maxEmailLen || !strings.Contains(out.Email[strings.LastIndex(out.Email, "@")+1:], ".") {
		errs["email"] = "Enter a valid email address."
	}

	out.Phone = phoneSeparators.Replace(strings.TrimSpace(in.Phone))
	if !e164.MatchString(out.Phone) {
		errs["phone"] = "Enter the number in international format, for example +8801XXXXXXXXX."
	}

	if in.Age < e.MinAge || in.Age > maxAge {
		errs["age"] = "Enter an age between " + strconv.Itoa(e.MinAge) + " and 120."
	}

	if len(errs) > 0 {
		return Input{}, errs
	}
	return out, nil
}

// digest binds an idempotency key to the exact request contents.
func digest(eventSlug string, in Input) []byte {
	b, _ := json.Marshal(struct {
		Event string `json:"event"`
		Input
	}{eventSlug, in})
	sum := sha256.Sum256(b)
	return sum[:]
}
