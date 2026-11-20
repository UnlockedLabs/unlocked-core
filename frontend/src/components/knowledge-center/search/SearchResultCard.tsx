import {
    BookOpen,
    FileText,
    Link as LinkIcon,
    Star,
    Video
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { SearchResultItem } from '@/types';
import { useToggleFavorite } from '@/hooks/useToggleFavorite';
import { resultLabel, resultTitle } from './resultDisplay';
import HighlightedSnippet from './HighlightedSnippet';

function ResultIcon({ item }: { item: SearchResultItem }) {
    const cls = 'size-5 text-brand';
    if (item.content_type === 'video') return <Video className={cls} />;
    if (item.content_type === 'link') return <LinkIcon className={cls} />;
    return item.match === 'content' ? (
        <FileText className={cls} />
    ) : (
        <BookOpen className={cls} />
    );
}

function openLabel(item: SearchResultItem): string {
    if (item.content_type === 'video') return 'Watch video';
    if (item.content_type === 'link') return 'Open link';
    return 'Open in library';
}

export default function SearchResultCard({
    item,
    favorited,
    canFavorite,
    onOpen,
    onFavoriteChanged
}: {
    item: SearchResultItem;
    favorited: boolean;
    canFavorite: boolean;
    onOpen: (item: SearchResultItem) => void;
    onFavoriteChanged: () => unknown;
}) {
    const { isFavorited, toggleFavorite } =
        useToggleFavorite(onFavoriteChanged);
    const title = resultTitle(item);
    const target = {
        id: item.content_id,
        type: item.content_type,
        favorited,
        // Title matches favorite the library itself (libraries/{id}/favorite);
        // only content matches are bookmarked as deep links (see `bookmark`).
        url: undefined,
        openContentProviderId: item.open_content_provider_id,
        bookmark:
            item.match === 'content' && item.content_type === 'library'
                ? { contentUrl: item.url, name: title }
                : undefined
    };
    const starred = isFavorited(target);

    return (
        <li
            className="card-block p-4 flex gap-4 cursor-pointer hover:bg-gray-50 transition-colors"
            onClick={() => onOpen(item)}
        >
            <div className="shrink-0 size-10 rounded-lg bg-brand/10 flex items-center justify-center">
                <ResultIcon item={item} />
            </div>
            <div className="flex-1 min-w-0">
                <p className="text-xs font-semibold tracking-wide text-gray-500">
                    {resultLabel(item)}
                </p>
                <h3 className="card-title-link mt-0.5">{title}</h3>
                {item.description && (
                    <p className="text-sm text-gray-600 mt-1">
                        <HighlightedSnippet text={item.description} />
                    </p>
                )}
                <Button
                    type="button"
                    variant="link"
                    className="px-0 mt-1 h-auto text-brand"
                    onClick={(e) => {
                        e.stopPropagation();
                        onOpen(item);
                    }}
                >
                    {openLabel(item)}
                </Button>
            </div>
            {canFavorite && (
                <button
                    type="button"
                    className="self-start p-1 rounded hover:bg-gray-100 transition-colors"
                    onClick={(e) => {
                        e.stopPropagation();
                        void toggleFavorite(target);
                    }}
                    aria-label={
                        starred ? 'Remove from favorites' : 'Add to favorites'
                    }
                    aria-pressed={starred}
                >
                    <Star
                        className={
                            starred
                                ? 'size-5 text-brand-gold fill-brand-gold'
                                : 'size-5 text-gray-400'
                        }
                    />
                </button>
            )}
        </li>
    );
}
