import type { ChatParams, ThinkingLevel, ThinkingStyle } from './types';

export const emptyParams = (): ChatParams => ({
	temperature: null,
	max_tokens: null,
	system_prompt: '',
	extra_body: null,
	thinking: '',
	thinking_style: ''
});

/** Fills fields missing from data saved before they existed. */
export const normalizeParams = (p: Partial<ChatParams> | null | undefined): ChatParams => ({
	...emptyParams(),
	...(p ?? {}),
	thinking: p?.thinking ?? '',
	thinking_style: p?.thinking_style ?? ''
});

export const THINKING_LEVELS: { value: ThinkingLevel; label: string }[] = [
	{ value: '', label: 'Server default' },
	{ value: 'off', label: 'Off' },
	{ value: 'low', label: 'Low' },
	{ value: 'medium', label: 'Medium' },
	{ value: 'high', label: 'High' }
];

export const THINKING_STYLES: { value: Exclude<ThinkingStyle, ''>; label: string; hint: string }[] = [
	{
		value: 'chat_template_kwargs',
		label: 'enable_thinking (Qwen3, DeepSeek on vLLM/SGLang)',
		hint: 'On/off only: low, medium and high all turn thinking on.'
	},
	{
		value: 'reasoning_effort',
		label: 'reasoning_effort (OpenAI, gpt-oss)',
		hint: 'Off is sent as reasoning_effort "none", which older models may reject.'
	}
];

/** A sensible mapping for a source: OpenAI's own API uses reasoning_effort. */
export const defaultThinkingStyle = (baseUrl: string | undefined): Exclude<ThinkingStyle, ''> =>
	baseUrl && /openai\.com/i.test(baseUrl) ? 'reasoning_effort' : 'chat_template_kwargs';

/** Short label for display, e.g. "thinking: high". */
export function thinkingLabel(p: ChatParams | null | undefined): string {
	if (!p?.thinking) return '';
	return `thinking ${p.thinking}`;
}
