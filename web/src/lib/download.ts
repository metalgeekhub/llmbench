// Browser download helpers.

/** Downloads a server file; the server names it via Content-Disposition. */
export function downloadUrl(url: string) {
	const a = document.createElement('a');
	a.href = url;
	a.download = '';
	document.body.appendChild(a);
	a.click();
	a.remove();
}

/** Downloads generated text as a file. */
export function downloadText(filename: string, text: string, type: string) {
	const url = URL.createObjectURL(new Blob([text], { type }));
	const a = document.createElement('a');
	a.href = url;
	a.download = filename;
	document.body.appendChild(a);
	a.click();
	a.remove();
	setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** A file-name-safe version of name (mirrors the server's naming). */
export function safeFilename(name: string): string {
	const base = name.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80);
	return base || 'llmbench';
}

/** True when the file looks like an exported run bundle rather than a definition file. */
export async function isRunBundle(file: File): Promise<boolean> {
	if (file.name.endsWith('.llmbench.json')) return true;
	const head = await file.slice(0, 4096).text();
	return /"format"\s*:\s*"llmbench"/.test(head);
}
