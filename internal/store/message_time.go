package store

import "time"

// messageTimeLayout is the one timestamp format a message row is written with.
// It is fixed width (nine fractional digits, always present) so that comparing
// created_at as text agrees with comparing the instants. Mixed formats do not:
// whole-second "...17Z" sorts after fractional "...17.824Z" because '.' < 'Z',
// and RFC3339Nano trims trailing zeros, which breaks the order the same way.
// Messages are read ORDER BY created_at, rowid, so every writer must use this
// (CW-20261011-0041).
const messageTimeLayout = "2006-01-02T15:04:05.000000000Z"

// messageTimestamp is the created_at for a message written now.
func messageTimestamp() string { return time.Now().UTC().Format(messageTimeLayout) }
