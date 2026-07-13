// The composer is a single text box, as a chat composer should be. The message's
// title — which the domain has, and which push notifications render as their
// headline — is derived from what was typed rather than asked for separately.

/** A first sentence longer than this is prose, not a headline; the message keeps none. */
const MAX_TITLE_CHARS = 100

export interface SplitMessage {
  title: string
  body: string
}

/**
 * Index just past the end of the first sentence, or -1 when the text is a single
 * unterminated sentence. A sentence ends at a line break, or at `.`/`!`/`?`
 * followed by whitespace or the end of the text — so "v2.14.0 is live" is not cut
 * at the version number.
 */
function firstSentenceEnd(text: string): number {
  const newline = text.indexOf('\n')
  const punctuation = /[.!?](\s|$)/.exec(text)
  const ends = [
    newline === -1 ? Infinity : newline,
    punctuation ? punctuation.index + 1 : Infinity,
  ]
  const end = Math.min(...ends)
  return end === Infinity ? -1 : end
}

/**
 * Splits typed text into a title (its first sentence) and a body (the rest).
 *
 * When the first sentence is too long to read as a headline, the whole text stays
 * in the body and the message goes out untitled — better than a bold paragraph.
 */
export function splitMessage(text: string): SplitMessage {
  const trimmed = text.trim()
  if (!trimmed) return { title: '', body: '' }

  const end = firstSentenceEnd(trimmed)
  if (end === -1) {
    return trimmed.length <= MAX_TITLE_CHARS
      ? { title: trimmed, body: '' }
      : { title: '', body: trimmed }
  }

  const title = trimmed.slice(0, end).trim()
  const body = trimmed.slice(end).trim()
  if (!title || title.length > MAX_TITLE_CHARS) return { title: '', body: trimmed }
  return { title, body }
}
