import useSWR from 'swr';
import { SearchChannel, SearchResultItem, ServerResponseMany } from '@/types';

// Stable empty list so effects keyed on `items` do not re-run every render.
const NO_ITEMS: SearchResultItem[] = [];

export const SEARCH_PATH = '/knowledge-center/search';
export const RESULTS_PER_PAGE = 10;
export const TYPEAHEAD_PER_PAGE = 6;

export interface KnowledgeSearchParams {
    q: string;
    page?: number;
    perPage?: number;
    /** Backend values: library | content | video | link. Empty = every kind. */
    types?: string[];
    /** Category ids. */
    tags?: string[];
    libraryIds?: string[];
}

/** Builds the `open-content/search` URL. Every value is URL-encoded. */
export function buildSearchUrl(p: KnowledgeSearchParams): string {
    const enc = encodeURIComponent;
    const parts = [
        `search=${enc(p.q)}`,
        `page=${enc(String(p.page ?? 1))}`,
        `per_page=${enc(String(p.perPage ?? RESULTS_PER_PAGE))}`
    ];
    (p.types ?? []).forEach((t) => parts.push(`types=${enc(t)}`));
    (p.tags ?? []).forEach((t) => parts.push(`tags=${enc(t)}`));
    (p.libraryIds ?? []).forEach((id) => parts.push(`library_id=${enc(id)}`));
    return `/api/open-content/search?${parts.join('&')}`;
}

/** Path of the results page for a query (plus any extra URL params). */
export function searchPagePath(q: string, extra?: URLSearchParams): string {
    const params = new URLSearchParams(extra);
    params.set('q', q);
    params.delete('page');
    return `${SEARCH_PATH}?${params.toString()}`;
}

/**
 * SWR over `open-content/search`. Pass `null` to skip the request (no query).
 * The API returns a single channel; `total` comes from the pagination meta.
 */
export function useKnowledgeSearch(params: KnowledgeSearchParams | null) {
    const q = params?.q.trim() ?? '';
    const swr = useSWR<ServerResponseMany<SearchChannel>>(
        params && q ? buildSearchUrl({ ...params, q }) : null,
        {
            keepPreviousData: true,
            // Show the error state quickly instead of after SWR's retries.
            errorRetryCount: 1
        }
    );
    const { data, isLoading, mutate } = swr;
    const error = swr.error as unknown;
    const channel = data?.data?.[0];
    return {
        items: channel?.items ?? NO_ITEMS,
        total: data?.meta?.total ?? 0,
        loaded: data !== undefined,
        isLoading,
        error,
        retry: () => void mutate()
    };
}
