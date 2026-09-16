// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package thinkingparser

import (
	"strings"
)

// State represents the current state of the thinking block parser FSM.
type State int

const (
	// StatePreContent: Initial state, buffering to detect opening tag.
	StatePreContent State = iota
	// StateInThinking: Inside thinking block, buffering until closing tag.
	StateInThinking
	// StateStreaming: Regular streaming, no more thinking block detection.
	StateStreaming
)

// Handling mode constants.
const (
	HandlingAsReasoningContent = "as_reasoning_content"
	HandlingRemove             = "remove"
	HandlingPass               = "pass"
	HandlingStripTags          = "strip_tags"
)

// Parser implements a three-state finite state machine for detecting
// and handling thinking blocks in streaming responses.
type Parser struct {
	state             State
	handling          string
	initialBufferSize int

	// Buffer for content while looking for opening tag
	initialBuffer strings.Builder

	// Buffer for content while looking for closing tag
	thinkingBuffer strings.Builder

	// Detected tags
	openTag  string
	closeTag string

	// Track if we've found a thinking block
	thinkingBlockFound bool

	// Track metadata for handling modes
	isFirstThinkingChunk bool

	// Tag names to detect
	openTags []string

	// Max tag length for cautious buffering
	maxTagLength int
}

// NewParser creates a new thinking block parser with default tags.
func NewParser(handling string, initialBufferSize int) *Parser {
	openTags := []string{"<thinking>", "<think>", "<reasoning>", "<thought>"}
	return NewParserWithTags(handling, initialBufferSize, openTags)
}

// NewParserWithTags creates a new thinking block parser with custom tags.
func NewParserWithTags(handling string, initialBufferSize int, openTags []string) *Parser {
	// Calculate max tag length: max(len(tag) for tag in openTags) * 2
	// This is used for cautious buffering to avoid splitting tags
	maxTagLen := 0
	for _, tag := range openTags {
		if len(tag) > maxTagLen {
			maxTagLen = len(tag)
		}
	}
	maxTagLength := maxTagLen * 2

	return &Parser{
		state:                StatePreContent,
		handling:             handling,
		initialBufferSize:    initialBufferSize,
		openTags:             openTags,
		maxTagLength:         maxTagLength,
		isFirstThinkingChunk: true,
	}
}

// Feed processes a chunk of content through the parser.
// It returns (thinking, content) where thinking is the part classified
// as thinking content and content is the regular output.
func (p *Parser) Feed(text string) (string, string) {
	if text == "" {
		return "", ""
	}

	switch p.state {
	case StatePreContent:
		return p.handlePreContent(text)
	case StateInThinking:
		return p.handleInThinking(text)
	case StateStreaming:
		return p.handleStreaming(text)
	}
	return "", ""
}

// Finish flushes any remaining buffered content at end of stream.
func (p *Parser) Finish() (string, string) {
	switch p.state {
	case StatePreContent:
		// No tag found, return initial buffer as content
		result := p.initialBuffer.String()
		p.initialBuffer.Reset()
		return "", result

	case StateInThinking:
		// Still inside thinking block, flush it
		result := p.thinkingBuffer.String()
		p.thinkingBuffer.Reset()
		thinking := p.processThinkingOutput(result, p.isFirstThinkingChunk, true)
		return thinking, ""

	case StateStreaming:
		// Already streaming, nothing to flush
		return "", ""
	}
	return "", ""
}

// Reset resets the parser to its initial state.
func (p *Parser) Reset() {
	p.state = StatePreContent
	p.initialBuffer.Reset()
	p.thinkingBuffer.Reset()
	p.openTag = ""
	p.closeTag = ""
	p.thinkingBlockFound = false
	p.isFirstThinkingChunk = true
}

// handlePreContent handles input while looking for an opening tag.
func (p *Parser) handlePreContent(text string) (string, string) {
	p.initialBuffer.WriteString(text)
	buffer := p.initialBuffer.String()

	// Strip leading whitespace for tag detection
	stripped := strings.TrimLeft(buffer, " \t\n\r")

	// Check if buffer starts with any opening tag
	for _, tag := range p.openTags {
		if strings.HasPrefix(stripped, tag) {
			// Tag found! Transition to IN_THINKING
			p.state = StateInThinking
			p.openTag = tag
			p.closeTag = "</" + tag[1:] // <thinking> -> </thinking>
			p.thinkingBlockFound = true

			// Content after the tag goes to thinking buffer
			contentAfterTag := stripped[len(tag):]
			p.thinkingBuffer.WriteString(contentAfterTag)
			p.initialBuffer.Reset()

			// Now process the thinking buffer for potential closing tag
			thinking, content := p.processThinkingBuffer()
			return thinking, content
		}
	}

	// Check if we might still be receiving the tag
	for _, tag := range p.openTags {
		if strings.HasPrefix(tag, stripped) && len(stripped) < len(tag) {
			// Could still be receiving the tag, keep buffering
			return "", ""
		}
	}

	// No tag found and buffer is too long or doesn't match any tag prefix
	if len(buffer) > p.initialBufferSize || !p.couldBeTagPrefix(stripped) {
		p.state = StateStreaming
		result := buffer
		p.initialBuffer.Reset()
		return "", result
	}

	return "", ""
}

// handleInThinking handles input while inside a thinking block.
func (p *Parser) handleInThinking(text string) (string, string) {
	p.thinkingBuffer.WriteString(text)
	return p.processThinkingBuffer()
}

// handleStreaming handles input after we've exited thinking mode.
func (p *Parser) handleStreaming(text string) (string, string) {
	return "", text
}

// processThinkingBuffer processes the thinking buffer, looking for closing tag.
func (p *Parser) processThinkingBuffer() (string, string) {
	buffer := p.thinkingBuffer.String()

	if p.closeTag == "" {
		return "", ""
	}

	// Check for closing tag
	closeIdx := strings.Index(buffer, p.closeTag)
	if closeIdx >= 0 {
		// Found closing tag
		thinkingContent := buffer[:closeIdx]
		afterTag := buffer[closeIdx+len(p.closeTag):]

		// Process the thinking content according to mode
		thinking := p.processThinkingOutput(thinkingContent, p.isFirstThinkingChunk, true)
		p.isFirstThinkingChunk = false

		// Transition to STREAMING
		p.state = StateStreaming
		p.thinkingBuffer.Reset()

		// Strip leading whitespace from content after tag
		content := strings.TrimLeft(afterTag, " \t\n\r")
		return thinking, content
	}

	// No closing tag yet - use cautious sending
	if len(buffer) > p.maxTagLength {
		sendPart := buffer[:len(buffer)-p.maxTagLength]
		remaining := buffer[len(buffer)-p.maxTagLength:]

		thinking := p.processThinkingOutput(sendPart, p.isFirstThinkingChunk, false)
		p.isFirstThinkingChunk = false

		p.thinkingBuffer.Reset()
		p.thinkingBuffer.WriteString(remaining)

		return thinking, ""
	}

	return "", ""
}

// couldBeTagPrefix checks if text could be the start of any opening tag.
func (p *Parser) couldBeTagPrefix(text string) bool {
	if text == "" {
		return true // Empty could be anything
	}
	for _, tag := range p.openTags {
		if strings.HasPrefix(tag, text) {
			return true
		}
	}
	return false
}

// processThinkingOutput applies the handling mode to thinking content.
func (p *Parser) processThinkingOutput(thinking string, isFirst, isLast bool) string {
	if thinking == "" {
		return ""
	}

	switch p.handling {
	case HandlingRemove:
		// Discard thinking content
		return ""

	case HandlingPass:
		// Add tags back
		prefix := ""
		suffix := ""
		if isFirst && p.openTag != "" {
			prefix = p.openTag
		}
		if isLast && p.closeTag != "" {
			suffix = p.closeTag
		}
		return prefix + thinking + suffix

	case HandlingStripTags:
		// Return content without tags
		return thinking

	case HandlingAsReasoningContent:
		// Return as-is for reasoning_content field
		return thinking

	default:
		// Default to as_reasoning_content
		return thinking
	}
}
