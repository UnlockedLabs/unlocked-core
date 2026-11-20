import { PaginationMeta } from './server';

export interface OpenContentProvider {
    id: number;
    title: string | null;
    url: string;
    thumbnail_url: string | null;
    currently_enabled: boolean;
    description: string | null;
}

export interface Video {
    id: number;
    title: string;
    description: string | null;
    channel_title: string;
    external_id: string;
    visibility_status: boolean;
    visible_facility_count?: number;
    thumbnail_url: string | null;
    open_content_provider_id: number;
    availability: 'available' | 'processing' | 'has_error';
    duration: number;
    created_at: string;
    updated_at: string;
    is_favorited: boolean;
    is_featured?: boolean;
    open_content_provider?: OpenContentProvider;
    video_download_attempts: VideoDownloadAttempt[];
    video_favorites: VideoFavorites[];
}

export interface VideoFavorites {
    user_id: number;
    video_id: number;
}

export interface VideoDownloadAttempt {
    id: number;
    video_id: number;
    error_message: string | null;
}

export const MAX_DOWNLOAD_ATTEMPTS = 5;

export interface Library {
    description: string | null;
    external_id: string | null;
    id: number;
    thumbnail_url: string | null;
    language: string | null;
    title: string;
    open_content_provider_id: number;
    updated_at: string;
    url: string;
    visibility_status: boolean;
    visible_facility_count?: number;
    open_content_provider: OpenContentProvider;
    is_favorited: boolean;
    is_featured?: boolean;
    tags?: string[];
}

export interface ContentFacilityVisibility {
    facility_id: number;
    facility_name: string;
    visibility_status: boolean;
}

export interface HelpfulLink {
    id: number;
    title: string;
    description: string;
    url: string;
    visibility_status: boolean;
    visible_facility_count?: number;
    thumbnail_url: string | null;
    open_content_provider_id: number;
    is_favorited: boolean;
    is_featured?: boolean;
}

export interface HelpfulLinkAndSort {
    helpful_links: HelpfulLink[];
    sort_order: string;
    meta: PaginationMeta;
}

export interface OpenContentItem {
    title: string;
    url: string;
    external_id: string | null;
    thumbnail_url: string | null;
    description?: string;
    visibility_status?: boolean;
    open_content_provider_id: number;
    content_id: number;
    content_type: string;
    provider_name?: string;
    channel_title?: string;
}

export interface OpenContentResponse extends OpenContentItem {
    is_featured: boolean;
    total_hours: number;
    total_minutes: number;
}

export type SearchMatch = 'title' | 'content';

export type SearchContentType = 'library' | 'video' | 'link';

/** One hit from `GET /api/open-content/search`. */
export interface SearchResultItem extends OpenContentItem {
    /** "title": the item's own title/description matched; "content": text inside a library page matched. */
    match: SearchMatch;
    content_type: SearchContentType;
    /** Always set by the search API; bookmarking an inside-library hit needs it. */
    open_content_provider_id: number;
    /** For content matches `title` is the library name and this is the page. */
    page_title?: string;
}

export interface SearchChannel {
    title: string;
    total_results: number | string;
    start_index: number | string;
    items_per_page: number | string;
    items?: SearchResultItem[] | null;
}

export enum OpenContentTabs {
    KIWIX = 'Libraries',
    VIDEOS = 'Videos',
    LINKS = 'Helpful Links',
    FAVORITES = 'Favorites'
}

export enum VideoAdminVisibility {
    'All Videos' = 'all',
    Visible = 'visible',
    Hidden = 'hidden'
}

export enum LibraryAdminVisibility {
    'All Libraries' = 'all',
    Visible = 'visible',
    Hidden = 'hidden',
    Featured = 'featured'
}

export enum FilterOpenContent {
    'Title (A to Z)' = '&order_by=title&order=ASC',
    'Title (Z to A)' = '&order_by=title&order=DESC',
    'Date Added (Newest First)' = '&order_by=created_at&order=DESC',
    'Date Added  (Oldest First)' = '&order_by=created_at&order=ASC'
}
