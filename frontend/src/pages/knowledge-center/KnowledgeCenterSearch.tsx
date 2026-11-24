import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import useSWR from 'swr';
import Breadcrumbs from '@/components/navigation/Breadcrumbs';
import { Pagination } from '@/components/Pagination';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import DidYouMean from '@/components/knowledge-center/search/DidYouMean';
import KnowledgeSearchBox from '@/components/knowledge-center/search/KnowledgeSearchBox';
import SearchFilters from '@/components/knowledge-center/search/SearchFilters';
import { typeOptions } from '@/components/knowledge-center/search/searchFilterOptions';
import {
    NoResults,
    PastLastPage,
    ResultsSkeleton,
    SearchError,
    SearchPrompt
} from '@/components/knowledge-center/search/SearchStates';
import SearchResultCard from '@/components/knowledge-center/search/SearchResultCard';
import {
    RESULTS_PER_PAGE,
    useKnowledgeSearch
} from '@/components/knowledge-center/search/useKnowledgeSearch';
import { useOpenSearchResult } from '@/components/knowledge-center/search/useOpenSearchResult';
import { useUrlPagination } from '@/hooks/useUrlPagination';
import { hasFeature, isAdministrator, useAuth } from '@/auth/useAuth';
import {
    FeatureAccess,
    OpenContentItem,
    SearchResultItem,
    ServerResponseMany
} from '@/types';

// Only one sort exists for now; the URL carries `sort` so more can be added.
const SORTS = [{ value: 'relevance', label: 'Most relevant' }];

const FAVORITES_KEY = '/api/open-content/favorites?page=1&per_page=500';

export default function KnowledgeCenterSearch() {
    const { user } = useAuth();
    const [searchParams, setSearchParams] = useSearchParams();
    const openResult = useOpenSearchResult();
    const canViewVideos =
        !!user && hasFeature(user, FeatureAccess.UploadVideoAccess);
    const canViewHelpfulLinks =
        !!user && hasFeature(user, FeatureAccess.HelpfulLinksAccess);
    const canFavorite = !!user && !isAdministrator(user);

    const q = (searchParams.get('q') ?? '').trim();
    const types = searchParams.getAll('types');
    const libraryIds = searchParams.getAll('library_id');
    const tag = searchParams.get('tag') ?? '';
    const sort = searchParams.get('sort') ?? SORTS[0].value;

    const { page, perPage, setPage, setPerPage } = useUrlPagination(
        1,
        RESULTS_PER_PAGE
    );

    const options = typeOptions(canViewVideos, canViewHelpfulLinks);

    const { items, total, loaded, isLoading, error, retry } =
        useKnowledgeSearch(
            q
                ? {
                      q,
                      page,
                      perPage,
                      types,
                      tags: tag ? [tag] : [],
                      libraryIds
                  }
                : null
        );

    const { data: favData, mutate: mutateFavorites } =
        useSWR<ServerResponseMany<OpenContentItem>>(FAVORITES_KEY);
    // Plain favorites are keyed by type + id; deep-link bookmarks of a library
    // page (stored with the page's /api/proxy/ URL) are keyed by URL.
    const favoriteKeys = useMemo(() => {
        const keys = new Set<string>();
        (favData?.data ?? []).forEach((f) => {
            const type =
                f.content_type === 'helpful_link' ? 'link' : f.content_type;
            if (type === 'library' && f.url?.includes('/api/proxy/')) {
                keys.add(`bookmark-${f.content_id}-${f.url}`);
            } else {
                keys.add(`${type}-${f.content_id}`);
            }
        });
        return keys;
    }, [favData?.data]);

    const isFavorite = (item: SearchResultItem) =>
        item.match === 'content' && item.content_type === 'library'
            ? favoriteKeys.has(`bookmark-${item.content_id}-${item.url}`)
            : favoriteKeys.has(`${item.content_type}-${item.content_id}`);

    // Any filter change goes back to page 1.
    const updateParams = (change: (p: URLSearchParams) => void) => {
        const next = new URLSearchParams(searchParams);
        change(next);
        next.delete('page');
        setSearchParams(next);
    };

    const start = total === 0 ? 0 : (page - 1) * perPage + 1;
    const end = Math.min(page * perPage, total);

    return (
        <div className="max-w-7xl mx-auto px-6 py-8">
            <Breadcrumbs
                items={[
                    { label: 'Knowledge Center', href: '/knowledge-center' },
                    { label: 'Search' }
                ]}
            />
            <div className="card-block p-4 mb-4">
                <KnowledgeSearchBox initialValue={q} />
            </div>

            {q && (
                <DidYouMean
                    query={q}
                    total={total}
                    loaded={loaded && !isLoading}
                    onPick={(s) => updateParams((p) => p.set('q', s))}
                />
            )}

            <div className="grid grid-cols-[16rem_1fr] gap-8">
                <SearchFilters
                    options={options}
                    types={types}
                    libraryIds={libraryIds}
                    tag={tag}
                    onTypesChange={(next) =>
                        updateParams((p) => {
                            p.delete('types');
                            next.forEach((t) => p.append('types', t));
                        })
                    }
                    onLibraryIdsChange={(ids) =>
                        updateParams((p) => {
                            p.delete('library_id');
                            ids.forEach((id) => p.append('library_id', id));
                        })
                    }
                    onTagChange={(next) =>
                        updateParams((p) => {
                            if (next) p.set('tag', next);
                            else p.delete('tag');
                        })
                    }
                />

                <section aria-live="polite">
                    {!q ? (
                        <SearchPrompt />
                    ) : error ? (
                        <SearchError onRetry={retry} />
                    ) : !loaded || isLoading ? (
                        <>
                            <p className="sr-only">Searching...</p>
                            <ResultsSkeleton />
                        </>
                    ) : total === 0 ? (
                        <NoResults query={q} />
                    ) : items.length === 0 ? (
                        <PastLastPage onFirstPage={() => setPage(1)} />
                    ) : (
                        <>
                            <div className="flex items-center justify-between mb-4">
                                <p className="text-sm text-gray-600">
                                    Results {start}-{end} of {total}
                                </p>
                                <div className="flex items-center gap-2 text-sm text-gray-600">
                                    <span id="kc-sort-label">Sort:</span>
                                    <Select
                                        value={sort}
                                        onValueChange={(v) =>
                                            updateParams((p) =>
                                                p.set('sort', v)
                                            )
                                        }
                                    >
                                        <SelectTrigger
                                            className="w-44"
                                            aria-labelledby="kc-sort-label"
                                        >
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {SORTS.map((s) => (
                                                <SelectItem
                                                    key={s.value}
                                                    value={s.value}
                                                >
                                                    {s.label}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>
                            </div>
                            <ul className="space-y-3 mb-6">
                                {items.map((item, i) => (
                                    <SearchResultCard
                                        key={`${item.content_type}-${item.match}-${item.content_id}-${item.url}-${i}`}
                                        item={item}
                                        favorited={isFavorite(item)}
                                        canFavorite={canFavorite}
                                        onOpen={(it) => void openResult(it)}
                                        onFavoriteChanged={mutateFavorites}
                                    />
                                ))}
                            </ul>
                            <Pagination
                                currentPage={page}
                                totalItems={total}
                                itemsPerPage={perPage}
                                onPageChange={setPage}
                                onItemsPerPageChange={setPerPage}
                                itemLabel="results"
                                perPageOptions={[10, 20, 40]}
                            />
                        </>
                    )}
                </section>
            </div>
        </div>
    );
}
