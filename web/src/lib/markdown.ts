// Renders model output (Markdown) to sanitized HTML. Model output is
// untrusted: it must always pass through DOMPurify before {@html}.

import DOMPurify from 'dompurify';
import { Marked } from 'marked';

const marked = new Marked({ gfm: true, breaks: false, async: false });

// Open links in a new tab without giving the target page access to this one.
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
	if (node.tagName === 'A' && node.hasAttribute('href')) {
		node.setAttribute('target', '_blank');
		node.setAttribute('rel', 'noopener noreferrer');
	}
});

export function renderMarkdown(src: string): string {
	const html = marked.parse(src) as string;
	// No images: a model could otherwise make the browser fetch attacker URLs
	// carrying conversation data in the query string.
	return DOMPurify.sanitize(html, { USE_PROFILES: { html: true }, FORBID_TAGS: ['img'] });
}
