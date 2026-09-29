package volumeapi

import "strings"

// contentDisposition builds the Content-Disposition value for one download,
// per RFC 6266. The name is sanitized first — control characters would break
// the header field, and a path separator would let the name escape the
// directory the browser saves into — and a name that sanitizes to nothing
// usable falls back to "download". A plain-ASCII name is carried in the
// quoted filename parameter, with `"` and `\` backslash-escaped; a name with
// any byte outside ASCII also carries filename*, its UTF-8 bytes
// percent-encoded per RFC 5987, with an ASCII approximation in filename as
// RFC 6266's Appendix D advises.
func contentDisposition(name string) string {
	name = sanitizeDownloadName(name)
	if isPlainASCII(name) {
		return `attachment; filename="` + escapeQuotedString(name) + `"`
	}
	return `attachment; filename="` + escapeQuotedString(asciiApproximation(name)) +
		`"; filename*=utf-8''` + percentEncodeRFC5987(name)
}

// sanitizeDownloadName removes what a Content-Disposition value must not
// carry: control characters (DEL included) and path separators, so no header
// can ever name another directory's file. A name that ends up empty or names
// a directory falls back to "download".
func sanitizeDownloadName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c < 0x20 || c == 0x7f:
			// Control characters would break the header line.
		case c == '/' || c == '\\':
			// A surviving separator would let the name escape the directory
			// the browser saves into.
			b.WriteByte('_')
		default:
			b.WriteByte(c)
		}
	}
	name = b.String()
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	return name
}

// isPlainASCII reports whether every byte of the name is US-ASCII.
func isPlainASCII(name string) bool {
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			return false
		}
	}
	return true
}

// escapeQuotedString backslash-escapes what RFC 7230's quoted-string cannot
// carry bare: the double quote and the backslash itself.
func escapeQuotedString(name string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
}

// asciiApproximation is the plain filename value for a non-ASCII name: the
// ASCII bytes survive and each non-ASCII rune becomes an underscore, the
// closest this package can get to the transliteration RFC 6266 suggests.
func asciiApproximation(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x80 {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// percentEncodeRFC5987 percent-encodes a name's bytes the RFC 5987 way: the
// attr-char set stays literal and every other byte — UTF-8's continuation
// bytes included — becomes %xx, lowercase as the RFC's examples are.
func percentEncodeRFC5987(name string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isRFC5987AttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// isRFC5987AttrChar reports whether c may appear literally in an RFC 5987
// ext-value: ALPHA / DIGIT / "!" / "#" / "$" / "&" / "+" / "-" / "." / "^" /
// "_" / "`" / "|" / "~".
func isRFC5987AttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
