import { useCallback, useState } from 'react';
import { toast } from 'sonner';
import API from '@/api/api';

export interface FavoriteTarget {
    id: number;
    type: 'library' | 'video' | 'link';
    favorited: boolean;
    url?: string;
    openContentProviderId?: number;
    /**
     * Set for a match found inside a library page: it is bookmarked as a
     * deep link (`open-content/{id}/bookmark`) rather than as the library.
     */
    bookmark?: { contentUrl: string; name: string };
}

/**
 * Optimistic favorite toggling shared by the Knowledge Center grid and the
 * search result cards. `onChanged` runs after a successful save so the caller
 * can refresh its own favorites data.
 */
export function useToggleFavorite(onChanged?: () => unknown) {
    const [pending, setPending] = useState<Map<string, boolean>>(new Map());

    const keyOf = (item: Pick<FavoriteTarget, 'id' | 'type' | 'bookmark'>) =>
        `${item.type}-${item.id}${item.bookmark ? `-${item.bookmark.contentUrl}` : ''}`;

    const isFavorited = useCallback(
        (item: FavoriteTarget) => {
            const key = keyOf(item);
            return pending.has(key) ? pending.get(key)! : item.favorited;
        },
        [pending]
    );

    const toggleFavorite = useCallback(
        async (item: FavoriteTarget) => {
            const key = keyOf(item);
            const current = pending.has(key)
                ? pending.get(key)!
                : item.favorited;
            const next = !current;

            setPending((prev) => new Map(prev).set(key, next));

            let endpoint = '';
            let payload: object = {};
            if (item.type === 'video') {
                endpoint = `videos/${item.id}/favorite`;
            } else if (item.type === 'link') {
                endpoint = `helpful-links/favorite/${item.id}`;
            } else if (item.bookmark) {
                // open-content's bookmark endpoint creates the favorite when
                // given `name` + `content_url` (no prior visit needed) and
                // removes it when given only `content_url`.
                endpoint = `open-content/${item.id}/bookmark`;
                payload = next
                    ? {
                          open_content_provider_id: item.openContentProviderId,
                          name: item.bookmark.name,
                          content_url: item.bookmark.contentUrl
                      }
                    : {
                          open_content_provider_id: item.openContentProviderId,
                          content_url: item.bookmark.contentUrl
                      };
            } else if (
                item.url?.includes('/api/proxy/') &&
                item.openContentProviderId
            ) {
                endpoint = `open-content/${item.id}/bookmark`;
                payload = {
                    open_content_provider_id: item.openContentProviderId,
                    content_url: item.url
                };
            } else {
                endpoint = `libraries/${item.id}/favorite`;
            }

            const resp = await API.put<object, object>(endpoint, payload);
            if (!resp.success) {
                setPending((prev) => new Map(prev).set(key, current));
                toast.error('Failed to update favorites');
            } else {
                // Refresh so the Favorites count (and the tab's list, when
                // active) stays in sync from any surface.
                await onChanged?.();
            }
        },
        [pending, onChanged]
    );

    return { isFavorited, toggleFavorite };
}
