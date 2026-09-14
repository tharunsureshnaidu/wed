// Package validate replaces the Java @Valid / jakarta.validation annotations.
//
// ponytail: hand-rolled rather than go-playground/validator. This app validates
// four shapes (non-empty, email, E.164 phone, length); the library's struct tags
// would be more code to wire up than the checks themselves.
package validate

import (
	"net/mail"
	"regexp"
	"strings"
)

// Mirrors the Java @Pattern(regexp = "^\\+?[1-9]\\d{1,14}$").
var phoneRe = regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)

// Both mirror Java's @Pattern on UpdateVendorProfileRequest exactly, message
// text included - the message is part of the API contract clients match on.
var urlRe = regexp.MustCompile(`^https?://.+`)
var upiRe = regexp.MustCompile(`^[\w.\-]{2,256}@[a-zA-Z]{2,64}$`)

type Errors []string

func (e Errors) Message() string { return strings.Join(e, "; ") }

func (e *Errors) Required(field, value string) {
	if strings.TrimSpace(value) == "" {
		*e = append(*e, field+" is required")
	}
}

func (e *Errors) Email(field string, value *string) {
	if value == nil || *value == "" {
		return
	}
	if _, err := mail.ParseAddress(*value); err != nil {
		*e = append(*e, "Invalid email format")
	}
}

func (e *Errors) Phone(field string, value *string) {
	if value == nil || *value == "" {
		return
	}
	if !phoneRe.MatchString(*value) {
		*e = append(*e, "Invalid phone number format")
	}
}

func (e *Errors) URL(field string, value *string) {
	if value == nil || *value == "" {
		return
	}
	if !urlRe.MatchString(*value) {
		*e = append(*e, "Must be a valid URL")
	}
}

func (e *Errors) UpiID(field string, value *string) {
	if value == nil || *value == "" {
		return
	}
	if !upiRe.MatchString(*value) {
		*e = append(*e, "Invalid UPI id")
	}
}

// MaxLength is Bean Validation's @Size(max=n) for an optional field: absent is
// valid, present is bounded. Counts runes, not bytes, so a 255-character name
// in Devanagari is not rejected for being 700 bytes.
func (e *Errors) MaxLength(field string, value *string, max int) {
	if value == nil {
		return
	}
	if n := len([]rune(*value)); n > max {
		*e = append(*e, field+" must be at most "+itoa(max)+" characters")
	}
}

func (e *Errors) Length(field, value string, min, max int) {
	if n := len(value); n < min || n > max {
		*e = append(*e, field+" must be between "+itoa(min)+" and "+itoa(max)+" characters")
	}
}

// Password is Length(8,100) with Java's wording. Bean Validation's
// @Size(min=8,max=100) renders "must be at least 8 characters" there, and the
// message is part of the API contract clients match on.
func (e *Errors) Password(field, value string) {
	if n := len(value); n < 8 || n > 100 {
		*e = append(*e, field+" must be at least 8 characters")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
