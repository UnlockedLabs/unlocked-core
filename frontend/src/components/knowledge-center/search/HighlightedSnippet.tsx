import { decodeHtmlEntities } from '@/lib/decodeHtmlEntities';

// Truncates to ~60 words, like Google snippets. A word inside <b>…</b> only
// counts once the tag closes so a highlight is never cut in half.
function truncateDescription(text: string, wordLimit = 60): string {
    const words = text.split(/\s+/);
    if (words.length <= wordLimit) return text;

    let wordCount = 0;
    let result = '';
    let inBoldTag = false;

    for (const word of words) {
        if (word.includes('<b>')) inBoldTag = true;
        if (!inBoldTag || word.includes('</b>')) wordCount++;
        if (word.includes('</b>')) inBoldTag = false;

        result += word + ' ';

        if (wordCount >= wordLimit) {
            result += '...';
            break;
        }
    }
    return result.trim();
}

/**
 * Renders a search snippet where the API wrapped matches in <b>…</b>.
 * Built from React nodes (never innerHTML), so nothing in the snippet can
 * inject markup.
 */
export default function HighlightedSnippet({
    text,
    className,
    wordLimit
}: {
    text: string;
    className?: string;
    wordLimit?: number;
}) {
    const pieces = truncateDescription(text, wordLimit).split(/<b>(.*?)<\/b>/g);
    return (
        <span className={className}>
            {pieces.map((piece, index) =>
                index % 2 === 1 ? (
                    <strong key={index} className="font-semibold text-gray-900">
                        {decodeHtmlEntities(piece)}
                    </strong>
                ) : (
                    decodeHtmlEntities(piece)
                )
            )}
        </span>
    );
}
