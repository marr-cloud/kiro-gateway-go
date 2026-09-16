// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

// blockIndex tracks Anthropic content-block indices across a stream. It
// mirrors the current_block_index / thinking_block_started /
// thinking_block_index / text_block_started / text_block_index locals in
// .upstream/kiro/streaming_anthropic.py:186-190, and the open/close
// transitions scattered through the event loop
// (streaming_anthropic.py:228-343,594-608).
//
// Anthropic requires each content block to open with exactly one
// content_block_start, carry zero or more content_block_delta events, and
// close with exactly one content_block_stop, with indices assigned
// sequentially starting at 0 as blocks open (never reused). Only one
// "thinking" block and one "text" block may be open at a time in this
// upstream (no interleaving of multiple text blocks); tool_use blocks are
// opened and closed atomically within a single Handle call, so they don't
// need persistent open-block state.
//
// Not safe for concurrent use, matching the Formatter that owns it.
type blockIndex struct {
	current int

	thinkingOpen bool
	thinkingAt   int

	textOpen bool
	textAt   int
}

// CloseThinking closes the open thinking block, if any, and advances the
// running index past it. Returns the closed block's index and whether one
// was actually open (streaming_anthropic.py:229-235,328-334,533-539,595-600).
func (b *blockIndex) CloseThinking() (idx int, closed bool) {
	if !b.thinkingOpen {
		return 0, false
	}
	idx = b.thinkingAt
	b.thinkingOpen = false
	b.current++
	return idx, true
}

// CloseText closes the open text block, if any, and advances the running
// index past it. Returns the closed block's index and whether one was
// actually open (streaming_anthropic.py:336-343,541-548,602-607).
func (b *blockIndex) CloseText() (idx int, closed bool) {
	if !b.textOpen {
		return 0, false
	}
	idx = b.textAt
	b.textOpen = false
	b.current++
	return idx, true
}

// OpenThinking opens a thinking block at the current running index if one
// isn't already open. Returns the block's index (existing or newly
// assigned) and whether this call actually opened it
// (streaming_anthropic.py:268-279).
func (b *blockIndex) OpenThinking() (idx int, opened bool) {
	if b.thinkingOpen {
		return b.thinkingAt, false
	}
	b.thinkingAt = b.current
	b.thinkingOpen = true
	return b.thinkingAt, true
}

// OpenText opens a text block at the current running index if one isn't
// already open. Returns the block's index (existing or newly assigned) and
// whether this call actually opened it
// (streaming_anthropic.py:237-248,302-313,542-548).
func (b *blockIndex) OpenText() (idx int, opened bool) {
	if b.textOpen {
		return b.textAt, false
	}
	b.textAt = b.current
	b.textOpen = true
	return b.textAt, true
}

// ReserveToolBlock returns the index for a new tool_use (or server_tool_use)
// block at the current running index, then advances past it. Tool blocks
// open, delta, and close within a single Handle call in this upstream, so
// there's no separate open/close pair to track
// (streaming_anthropic.py:486-519).
func (b *blockIndex) ReserveToolBlock() int {
	idx := b.current
	b.current++
	return idx
}

// ThinkingOpen reports whether a thinking block is currently open.
func (b *blockIndex) ThinkingOpen() bool { return b.thinkingOpen }

// ThinkingIndex returns the currently open thinking block's index. Only
// meaningful when ThinkingOpen() is true.
func (b *blockIndex) ThinkingIndex() int { return b.thinkingAt }

// TextOpen reports whether a text block is currently open.
func (b *blockIndex) TextOpen() bool { return b.textOpen }

// TextIndex returns the currently open text block's index. Only meaningful
// when TextOpen() is true.
func (b *blockIndex) TextIndex() int { return b.textAt }
