package sanitizer_test

import (
	"strings"
	"testing"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/faq/sanitizer"
)

func TestSanitizeHTML_PreservesLegitimateRichText(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "paragraphs, ordered list, and bold",
			input: "<p>To book a marriage hall, follow these steps:</p><ol><li>Browse available venues.</li><li>Select your preferred hall.</li><li>Choose your event date.</li><li>Submit your booking request.</li></ol><p>Our team will help you with the booking process.</p>",
		},
		{
			name:  "unordered list and strong note",
			input: "<p>Follow these steps to book your venue:</p><ul><li>Choose a venue.</li><li>Select your event date.</li><li>Submit your booking request.</li></ul><p><strong>Note:</strong> Booking confirmation depends on availability.</p>",
		},
		{
			name:  "headings, quotes, italics, links, images",
			input: "<h2>Venue Guidelines</h2><p>Here is an <em>important</em> quote:</p><blockquote>Book early to secure discounts.</blockquote><p>Visit <a href=\"https://example.com/halls\" target=\"_blank\">our website</a> for photos.</p><p><img src=\"https://example.com/images/hall.jpg\" alt=\"Grand Ballroom\" width=\"600\" height=\"400\" /></p>",
		},
		{
			name:  "tables and safe inline styles",
			input: "<table class=\"table-striped\"><thead><tr><th>Tier</th><th>Price</th></tr></thead><tbody><tr><td>Silver</td><td style=\"color: green; font-weight: bold;\">Rs. 50,000</td></tr></tbody></table>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizer.SanitizeHTML(tc.input)
			if got != tc.input {
				t.Errorf("SanitizeHTML modified legitimate HTML.\nExpected:\n%s\nGot:\n%s", tc.input, got)
			}
		})
	}
}

func TestSanitizeHTML_NeutralizesXSS(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		mustContain []string
		mustNotHave []string
	}{
		{
			name:        "strip script tag and inner script content",
			input:       "<p>Welcome</p><script>alert('XSS');</script><p>Thank you</p>",
			mustContain: []string{"<p>Welcome</p>", "<p>Thank you</p>"},
			mustNotHave: []string{"<script", "</script>", "alert"},
		},
		{
			name:        "strip uppercase SCRIPT tag",
			input:       "<p>Welcome</p><SCRIPT SRC=\"http://evil.com/xss.js\"></SCRIPT>",
			mustContain: []string{"<p>Welcome</p>"},
			mustNotHave: []string{"SCRIPT", "evil.com"},
		},
		{
			name:        "strip iframe, object, and embed",
			input:       "<p>Hi</p><iframe src=\"http://evil.com\"></iframe><object data=\"bad\"></object><embed src=\"bad\"/>",
			mustContain: []string{"<p>Hi</p>"},
			mustNotHave: []string{"iframe", "object", "embed", "evil.com"},
		},
		{
			name:        "strip event handlers (onerror, onload, onclick)",
			input:       `<img src="https://example.com/photo.jpg" onerror="alert(1)" onload="evil()" onclick="bad()">`,
			mustContain: []string{`src="https://example.com/photo.jpg"`},
			mustNotHave: []string{"onerror", "onload", "onclick", "alert(1)", "evil()", "bad()"},
		},
		{
			name:        "neutralize javascript: URL in href",
			input:       `<a href="javascript:alert(document.cookie)">Click here</a>`,
			mustContain: []string{`href="#"`, "Click here"},
			mustNotHave: []string{"javascript:", "alert", "document.cookie"},
		},
		{
			name:        "neutralize javascript: URL with casing and whitespace in href",
			input:       `<a href="  JAVASCRIPT:alert(1)  ">Click here</a>`,
			mustContain: []string{`href="#"`, "Click here"},
			mustNotHave: []string{"JAVASCRIPT:", "alert(1)"},
		},
		{
			name:        "strip dangerous style expressions",
			input:       `<div style="color: red; width: expression(alert(1));">Text</div>`,
			mustContain: []string{"Text", "<div", "</div>"},
			mustNotHave: []string{"expression", "alert(1)"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizer.SanitizeHTML(tc.input)
			for _, exp := range tc.mustContain {
				if !strings.Contains(got, exp) {
					t.Errorf("SanitizeHTML result missing expected content: %q in %q", exp, got)
				}
			}
			for _, notExp := range tc.mustNotHave {
				if strings.Contains(got, notExp) {
					t.Errorf("SanitizeHTML result contains forbidden content: %q in %q", notExp, got)
				}
			}
		})
	}
}

func TestSanitizeHTML_Idempotent(t *testing.T) {
	input := `<p>Valid <strong style="color: blue;">FAQ</strong> with <a href="https://example.com">link</a>.</p>`
	first := sanitizer.SanitizeHTML(input)
	second := sanitizer.SanitizeHTML(first)
	third := sanitizer.SanitizeHTML(second)

	if first != second || second != third {
		t.Fatalf("SanitizeHTML is not idempotent:\n1st: %s\n2nd: %s\n3rd: %s", first, second, third)
	}
}
