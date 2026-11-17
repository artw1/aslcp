// Small CodeMirror 6 stream mode for Asterisk .conf files:
// [section](template), key = value / key => value, ; comments (and ;-- ... --; blocks), #include / #tryinclude / #exec.
import { StreamLanguage, HighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { tags as t } from '@lezer/highlight';

interface St { depth: number; value: boolean }

// Same rules as internal/astconf: ";--" opens a (nestable) block comment,
// but ";---" does not; "--;" closes one.
const blockOpen = /^;--(?!-)/;

const language = StreamLanguage.define<St>({
  name: 'asterisk',
  startState: () => ({ depth: 0, value: false }),
  copyState: (s) => ({ ...s }),
  token(stream, st) {
    if (stream.sol()) st.value = false;
    if (st.depth > 0) {
      while (!stream.eol() && st.depth > 0) {
        if (stream.match('--;')) st.depth--;
        else if (stream.match(blockOpen)) st.depth++;
        else stream.next();
      }
      return 'comment';
    }
    if (stream.eatSpace()) return null;
    if (stream.peek() === ';') {
      if (stream.match(blockOpen)) { st.depth = 1; return 'comment'; }
      stream.skipToEnd(); return 'comment';
    }
    if (stream.sol() || stream.string.slice(0, stream.pos).trim() === '') {
      if (stream.match(/^#(?:try)?include\b|^#exec\b/)) { st.value = true; return 'keyword'; }
      if (stream.peek() === '[') { stream.match(/^\[[^\]]*\]?/); return 'heading'; }
    }
    if (!st.value) {
      if (stream.match(/^\((?:[^)]*)\)/)) return 'typeName'; // [section](template) / (!)
      if (stream.match(/^=>?/)) { st.value = true; return 'operator'; }
      const k = stream.match(/^[^=;]*?(?=\s*=|\s*;|\s*$)/) as RegExpMatchArray | null;
      if (k && k[0].length) return 'propertyName';
      stream.next(); return null;
    }
    if (stream.match(/^-?\d+(?:\.\d+)?(?=\s|;|$)/)) return 'number';
    if (stream.match(/^(?:yes|no|on|off|true|false)(?=\s|;|$)/i)) return 'atom';
    if (stream.match(/^\$\{[^}]*\}/)) return 'variableName';
    // plain value text up to a comment or the next variable
    stream.match(/^[^;$\s]+|^\$/);
    return 'string';
  },
  languageData: { commentTokens: { line: ';' } },
});

// Colours come from CSS variables (--cm-*, defined in CodeEditor.svelte for both themes).
const style = HighlightStyle.define([
  { tag: t.heading, color: 'var(--cm-section)', fontWeight: '600' },
  { tag: t.typeName, color: 'var(--cm-template)' },
  { tag: t.propertyName, color: 'var(--cm-key)' },
  { tag: t.operator, color: 'var(--cm-op)' },
  { tag: t.comment, color: 'var(--cm-comment)', fontStyle: 'italic' },
  { tag: t.keyword, color: 'var(--cm-directive)', fontWeight: '600' },
  { tag: t.number, color: 'var(--cm-number)' },
  { tag: t.atom, color: 'var(--cm-number)' },
  { tag: t.variableName, color: 'var(--cm-var)' },
  { tag: t.string, color: 'var(--cm-value)' },
]);

export const asterisk = [language, syntaxHighlighting(style)];
