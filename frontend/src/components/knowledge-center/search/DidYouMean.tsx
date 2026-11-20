import { useState } from 'react';
import useSWR from 'swr';

const VISIBLE_CHIPS = 3;

// Suggestions come from Google's suggest service, proxied by open-content:
// either the raw tuple [query, [suggestions...], ...] or wrapped in `data`.
type SuggestionResponse = unknown;

function suggestionsFrom(resp: SuggestionResponse): string[] {
    const tuple = Array.isArray(resp)
        ? resp
        : (resp as { data?: unknown } | undefined)?.data;
    const list = Array.isArray(tuple) ? (tuple[1] as unknown) : undefined;
    return Array.isArray(list)
        ? list.filter((s): s is string => typeof s === 'string')
        : [];
}

// Old rule: suggest when the query has more than one word, a word longer than
// 8 characters, or it found nothing.
function shouldSuggest(query: string, total: number): boolean {
    const words = query.trim().split(/\s+/).filter(Boolean);
    return words.length > 1 || words.some((w) => w.length > 8) || total === 0;
}

/**
 * "Did you mean" chips. Kept in one component so it can be switched off in one
 * place: set `ENABLED` to false (or stop rendering <DidYouMean />).
 */
const ENABLED = true;

export default function DidYouMean({
    query,
    total,
    loaded,
    onPick
}: {
    query: string;
    total: number;
    /** True once the search for `query` has returned. */
    loaded: boolean;
    onPick: (suggestion: string) => void;
}) {
    const [showAll, setShowAll] = useState(false);
    const trimmed = query.trim();
    const active =
        ENABLED && loaded && trimmed !== '' && shouldSuggest(trimmed, total);
    const { data } = useSWR<SuggestionResponse>(
        active
            ? `/api/open-content/suggestions?query=${encodeURIComponent(trimmed)}`
            : null
    );

    const suggestions = suggestionsFrom(data).filter(
        (s) => s.toLowerCase() !== trimmed.toLowerCase()
    );
    if (!active || suggestions.length === 0) return null;

    const shown = showAll ? suggestions : suggestions.slice(0, VISIBLE_CHIPS);
    return (
        <div className="flex flex-wrap items-center gap-2 text-sm mb-4">
            <span className="text-gray-600">Did you mean:</span>
            {shown.map((s) => (
                <button
                    key={s}
                    type="button"
                    onClick={() => onPick(s)}
                    className="px-3 py-1 rounded-full border border-gray-200 bg-white text-brand hover:bg-gray-50"
                >
                    {s}
                </button>
            ))}
            {!showAll && suggestions.length > VISIBLE_CHIPS && (
                <button
                    type="button"
                    onClick={() => setShowAll(true)}
                    className="text-brand hover:underline"
                >
                    More
                </button>
            )}
        </div>
    );
}
