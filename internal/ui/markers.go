package ui

// Single-glyph markers used in CLI reports. The glyph is semantic; the colour
// comes from the Style paired with it (see table below).
//
//   Marker       Glyph  Style         Meaning
//   MarkPresent  ✓      StyleSuccess  exists / succeeded / enabled
//   MarkAbsent   ✗      StyleHint     missing / disabled (neutral — no failure)
//   MarkFail     ✗      StyleError    hard failure (same glyph, colour disambiguates)
//   MarkPartial  ·      StyleHint     partial state / listed without a state
//   MarkStarred  ★      StyleSuccess  active / default choice
//   MarkPending  ~      StyleHint     queued / diff / not-yet-applied
//   MarkWarn     ⚠      StyleWarning  attention needed, non-fatal
//
// Callers should not define inline glyph literals; use these constants so the
// marker alphabet stays searchable and consistent. Printer.Bullet applies the
// colour layer automatically via (marker, style) tuples.
const (
	MarkPresent = "✓"
	MarkAbsent  = "✗"
	MarkFail    = "✗"
	MarkPartial = "·"
	MarkStarred = "★"
	MarkPending = "~"
	MarkWarn    = "⚠"
)
