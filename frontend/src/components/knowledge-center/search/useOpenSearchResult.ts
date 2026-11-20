import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import API from '@/api/api';
import { toExternalUrl } from '@/lib/utils';
import { SearchResultItem } from '@/types';

/**
 * Opens a search hit: library title match -> library viewer; content match ->
 * library viewer at the deep page (LibraryViewer reads `state.url`); video ->
 * video viewer; helpful link -> activity call, then a new tab.
 */
export function useOpenSearchResult() {
    const navigate = useNavigate();
    return useCallback(
        async (item: SearchResultItem) => {
            if (item.content_type === 'video') {
                navigate(`/viewer/videos/${item.content_id}`);
            } else if (item.content_type === 'link') {
                const resp = await API.put<{ url: string }, object>(
                    `helpful-links/activity/${item.content_id}`,
                    {}
                );
                const url =
                    resp.success && resp.data
                        ? ((resp.data as { url?: string }).url ?? item.url)
                        : item.url;
                window.open(toExternalUrl(url), '_blank');
            } else if (item.match === 'content') {
                navigate(`/viewer/libraries/${item.content_id}`, {
                    state: { url: item.url }
                });
            } else {
                navigate(`/viewer/libraries/${item.content_id}`);
            }
        },
        [navigate]
    );
}
