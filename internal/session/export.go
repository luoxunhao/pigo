// This file implements session export/import (US-008, #124): a session can be
// exported to a self-contained JSONL or HTML file, and a JSONL export can be
// imported back as a fresh, resumable session. The JSONL form is the same
// role-discriminated schema the store persists, so an export → import round-trip
// is lossless (message contents and the id/parentId tree survive verbatim); the
// HTML form is a read-only, self-contained transcript with inline styles and no
// external network resources, suitable for sharing.
package session

import (
	"io"
	"strings"
)


// WriteHTML writes a self-contained HTML transcript of the session: inline CSS
// only (no external stylesheets, fonts, scripts, or network resources), role
// color-coding, and tool-call/result blocks. All message text is HTML-escaped
// so a transcript containing markup or a crafted "</script>" cannot break out of
// its container or inject active content (defensive against a hostile session).
func WriteHTML(w io.Writer, header SessionHeader, entries []Entry) error {
	var b strings.Builder
	b.WriteString(htmlHead(header))
	for _, e := range entries {
		b.WriteString(renderEntryHTML(e))
	}
	b.WriteString(htmlFoot())
	_, err := io.WriteString(w, b.String())
	return err
}
