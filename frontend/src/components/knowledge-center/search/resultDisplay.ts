import { SearchResultItem } from '@/types';
import { decodeHtmlEntities } from '@/lib/decodeHtmlEntities';

export function resultLabel(item: SearchResultItem): string {
    if (item.content_type === 'video') return 'VIDEO';
    if (item.content_type === 'link') return 'HELPFUL LINK';
    if (item.match === 'content') {
        return `IN ${decodeHtmlEntities(item.title).toUpperCase()}`;
    }
    return 'LIBRARY MATCH';
}

export function resultTitle(item: SearchResultItem): string {
    const title =
        item.match === 'content' && item.page_title
            ? item.page_title
            : item.title;
    return decodeHtmlEntities(title);
}
