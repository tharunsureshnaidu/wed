package sanitizer

import (
	"regexp"
	"strings"
)

var (
	// Paired dangerous tags whose entire body must be discarded
	scriptTagRe = regexp.MustCompile(`(?is)<\s*script\b[^>]*>.*?<\s*/\s*script\s*>`)
	styleTagRe  = regexp.MustCompile(`(?is)<\s*style\b[^>]*>.*?<\s*/\s*style\s*>`)
	iframeTagRe = regexp.MustCompile(`(?is)<\s*iframe\b[^>]*>.*?<\s*/\s*iframe\s*>`)
	objectTagRe = regexp.MustCompile(`(?is)<\s*object\b[^>]*>.*?<\s*/\s*object\s*>`)
	embedTagRe  = regexp.MustCompile(`(?is)<\s*embed\b[^>]*>.*?<\s*/\s*embed\s*>`)
	appletTagRe = regexp.MustCompile(`(?is)<\s*applet\b[^>]*>.*?<\s*/\s*applet\s*>`)
	formTagRe   = regexp.MustCompile(`(?is)<\s*form\b[^>]*>.*?<\s*/\s*form\s*>`)

	// Remaining standalone dangerous tags (self-closing, unpaired or malformed)
	dangerousStandaloneRe = regexp.MustCompile(`(?i)</?\s*(?:script|style|iframe|object|embed|applet|form|meta|link|base|input|button)\b[^>]*>`)

	// Event handler attributes such as onload=, onerror=, onclick=, onmouseover=, etc.
	eventAttrRe = regexp.MustCompile(`(?i)\s+on[a-z0-9_-]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)

	// HTML comments that could hide browser-specific execution directives (e.g., conditional comments)
	htmlCommentRe = regexp.MustCompile(`(?s)<!--\[if.*?-->`)

	// URL attributes: href, src, action, formaction
	urlAttrRe = regexp.MustCompile(`(?i)\b(href|src|action|formaction)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)

	// Style attributes containing executable CSS expressions or script bindings
	styleAttrRe = regexp.MustCompile(`(?i)\bstyle\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)

	// Dangerous URL schemes
	dangerousSchemeRe = regexp.MustCompile(`(?i)^\s*(?:javascript|vbscript|data\s*:\s*text/html)\s*:`)

	// Dangerous style expressions
	dangerousStyleRe = regexp.MustCompile(`(?i)(?:expression\s*\(|javascript\s*:|behavior\s*:|-moz-binding\s*:|url\s*\(\s*['"]?\s*javascript)`)
)

// SanitizeHTML cleans rich-text editor HTML content by removing executable scripts,
// event handlers, and dangerous URL schemes, while strictly preserving legitimate
// formatting tags (e.g., p, h1-h6, strong, em, ul, ol, li, a, img, table, blockquote).
//
// The function is idempotent: SanitizeHTML(SanitizeHTML(input)) == SanitizeHTML(input).
func SanitizeHTML(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}

	res := html

	// 1. Remove dangerous block elements and their inner content
	res = scriptTagRe.ReplaceAllString(res, "")
	res = styleTagRe.ReplaceAllString(res, "")
	res = iframeTagRe.ReplaceAllString(res, "")
	res = objectTagRe.ReplaceAllString(res, "")
	res = embedTagRe.ReplaceAllString(res, "")
	res = appletTagRe.ReplaceAllString(res, "")
	res = formTagRe.ReplaceAllString(res, "")

	// 2. Remove any remaining standalone or malformed dangerous tags
	res = dangerousStandaloneRe.ReplaceAllString(res, "")

	// 3. Remove conditional comments
	res = htmlCommentRe.ReplaceAllString(res, "")

	// 4. Strip inline event handlers from all remaining tags (e.g. onerror, onload, onclick)
	res = eventAttrRe.ReplaceAllString(res, "")

	// 5. Sanitize URL attributes (href, src, action)
	res = urlAttrRe.ReplaceAllStringFunc(res, func(match string) string {
		submatches := urlAttrRe.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		attrName := strings.ToLower(submatches[1])
		val := submatches[3]
		if val == "" {
			val = submatches[4]
		}
		if val == "" {
			val = submatches[5]
		}

		// Normalize value to check for dangerous schemes
		trimmedVal := strings.TrimSpace(val)
		// Check for javascript:, vbscript:, data:text/html
		if dangerousSchemeRe.MatchString(trimmedVal) {
			if attrName == "href" {
				return `href="#"`
			}
			return ""
		}
		return match
	})

	// 6. Sanitize inline styles: strip style attributes containing expressions or javascript:
	res = styleAttrRe.ReplaceAllStringFunc(res, func(match string) string {
		submatches := styleAttrRe.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		val := submatches[2]
		if val == "" {
			val = submatches[3]
		}
		if val == "" {
			val = submatches[4]
		}

		if dangerousStyleRe.MatchString(val) {
			return ""
		}
		return match
	})

	return strings.TrimSpace(res)
}
