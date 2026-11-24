import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useDebounceValue } from 'usehooks-ts';
import {
    BookOpen,
    FileText,
    Link as LinkIcon,
    Search,
    Video,
    X
} from 'lucide-react';
import { decodeHtmlEntities } from '@/lib/decodeHtmlEntities';
import { cn } from '@/lib/utils';
import { SearchResultItem } from '@/types';
import HighlightedSnippet from './HighlightedSnippet';
import { resultTitle } from './resultDisplay';
import {
    SEARCH_PATH,
    TYPEAHEAD_PER_PAGE,
    searchPagePath,
    useKnowledgeSearch
} from './useKnowledgeSearch';
import { useOpenSearchResult } from './useOpenSearchResult';
import { Skeleton } from '@/components/ui/skeleton';

const DEBOUNCE_MS = 300;

type PanelOption =
    | { kind: 'result'; id: string; item: SearchResultItem }
    | { kind: 'all'; id: string };

interface Group {
    label: string;
    options: PanelOption[];
}

function isTypingTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) return false;
    return (
        target.isContentEditable ||
        ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
    );
}

function ResultIcon({ item }: { item: SearchResultItem }) {
    const cls = 'size-4 text-brand shrink-0 mt-0.5';
    if (item.content_type === 'video') return <Video className={cls} />;
    if (item.content_type === 'link') return <LinkIcon className={cls} />;
    return item.match === 'content' ? (
        <FileText className={cls} />
    ) : (
        <BookOpen className={cls} />
    );
}

export default function KnowledgeSearchBox({
    initialValue = '',
    className
}: {
    /** Seeds (and re-syncs) the input, e.g. with the results page's `q`. */
    initialValue?: string;
    className?: string;
}) {
    const navigate = useNavigate();
    const location = useLocation();
    const [searchParams] = useSearchParams();
    const openResult = useOpenSearchResult();

    const inputRef = useRef<HTMLInputElement>(null);
    const baseId = useId();
    const listboxId = `${baseId}-listbox`;
    const optionId = (i: number) => `${baseId}-opt-${i}`;

    const [value, setValue] = useState(initialValue);
    const [open, setOpen] = useState(false);
    const [activeIndex, setActiveIndex] = useState(-1);
    const [debounced] = useDebounceValue(value.trim(), DEBOUNCE_MS);

    useEffect(() => {
        setValue(initialValue);
    }, [initialValue]);

    // "/" focuses the box unless the user is already typing in a field.
    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (
                e.key !== '/' ||
                e.ctrlKey ||
                e.metaKey ||
                e.altKey ||
                isTypingTarget(e.target)
            ) {
                return;
            }
            e.preventDefault();
            inputRef.current?.focus();
        };
        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, []);

    const typed = value.trim() !== '';
    const { items, total, loaded, isLoading, error } = useKnowledgeSearch(
        typed && debounced
            ? { q: debounced, perPage: TYPEAHEAD_PER_PAGE }
            : null
    );
    // keepPreviousData keeps `loaded` true while a new search runs; isLoading
    // is what tells the results apart from the previous query's.
    const settled = typed && loaded && !isLoading && debounced === value.trim();

    const groups: Group[] = useMemo(() => {
        const result = (item: SearchResultItem, i: number): PanelOption => ({
            kind: 'result',
            id: `r-${item.content_type}-${item.match}-${item.content_id}-${i}`,
            item
        });
        if (!settled) return [];
        const by = (pred: (i: SearchResultItem) => boolean) =>
            items.map((it, i) => ({ it, i })).filter(({ it }) => pred(it));
        const build = (
            label: string,
            pred: (i: SearchResultItem) => boolean
        ) => ({
            label,
            options: by(pred).map(({ it, i }) => result(it, i))
        });
        const list = [
            build(
                'Libraries',
                (i) => i.match === 'title' && i.content_type === 'library'
            ),
            build('Inside libraries', (i) => i.match === 'content'),
            build('Videos', (i) => i.content_type === 'video'),
            build('Helpful links', (i) => i.content_type === 'link')
        ].filter((g) => g.options.length > 0);
        if (list.length > 0) {
            list.push({
                label: '',
                options: [{ kind: 'all', id: 'see-all' }]
            });
        }
        return list;
    }, [settled, items]);

    const flat = groups.flatMap((g) => g.options);

    useEffect(() => {
        setActiveIndex(-1);
    }, [typed, items]);

    const close = () => {
        setOpen(false);
        setActiveIndex(-1);
    };

    const submit = (term: string) => {
        const q = term.trim();
        if (!q) return;
        setValue(q);
        close();
        // Keep the filters when searching again from the results page.
        const keep =
            location.pathname === SEARCH_PATH
                ? new URLSearchParams(searchParams)
                : undefined;
        keep?.delete('q');
        navigate(searchPagePath(q, keep));
    };

    const activate = (opt: PanelOption) => {
        if (opt.kind === 'result') {
            close();
            void openResult(opt.item);
        } else {
            submit(value);
        }
    };

    const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault();
            if (!open) setOpen(true);
            if (flat.length === 0) return;
            const step = e.key === 'ArrowDown' ? 1 : -1;
            setActiveIndex((i) =>
                i < 0
                    ? step > 0
                        ? 0
                        : flat.length - 1
                    : (i + step + flat.length) % flat.length
            );
        } else if (e.key === 'Enter') {
            e.preventDefault();
            const opt = activeIndex >= 0 ? flat[activeIndex] : undefined;
            if (open && opt) activate(opt);
            else submit(value);
        } else if (e.key === 'Escape') {
            if (open) {
                e.preventDefault();
                close();
            }
        }
    };

    // Nothing is shown for an empty box; searches are never stored.
    const showPanel = open && typed;
    let runningIndex = -1;

    return (
        <div
            className={cn('relative', className)}
            onBlur={(e) => {
                if (!e.currentTarget.contains(e.relatedTarget)) close();
            }}
        >
            <div className="relative">
                <Search className="input-icon-left size-5" aria-hidden="true" />
                <input
                    ref={inputRef}
                    type="text"
                    role="combobox"
                    aria-label="Search the Knowledge Center"
                    aria-expanded={showPanel}
                    aria-controls={listboxId}
                    aria-autocomplete="list"
                    aria-activedescendant={
                        showPanel && activeIndex >= 0
                            ? optionId(activeIndex)
                            : undefined
                    }
                    autoComplete="off"
                    placeholder="Search titles and everything inside libraries..."
                    value={value}
                    onChange={(e) => {
                        setValue(e.target.value);
                        setActiveIndex(-1);
                        setOpen(true);
                    }}
                    onFocus={() => setOpen(true)}
                    onKeyDown={onKeyDown}
                    className="flex h-10 w-full rounded-md border border-input bg-input-background pl-10 pr-20 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                />
                <div className="absolute right-3 top-1/2 -translate-y-1/2 flex items-center gap-2">
                    {value ? (
                        <button
                            type="button"
                            aria-label="Clear search"
                            className="text-gray-400 hover:text-gray-700"
                            onClick={() => {
                                setValue('');
                                inputRef.current?.focus();
                            }}
                        >
                            <X className="size-4" />
                        </button>
                    ) : (
                        <kbd
                            className="hidden sm:inline rounded border border-gray-200 px-1.5 text-xs text-gray-500"
                            aria-hidden="true"
                        >
                            /
                        </kbd>
                    )}
                </div>
            </div>

            {showPanel && (
                <div
                    className="absolute z-30 mt-1 w-full rounded-lg border border-gray-200 bg-white shadow-lg max-h-[28rem] overflow-y-auto"
                    onMouseDown={(e) => e.preventDefault()}
                >
                    <div id={listboxId} role="listbox" aria-label="Suggestions">
                        {groups.map((group) => (
                            <div
                                key={group.label || 'footer'}
                                role="group"
                                aria-label={group.label || 'See all results'}
                            >
                                {group.label && (
                                    <p
                                        className="px-4 pt-3 pb-1 text-xs font-semibold tracking-wide text-gray-500"
                                        aria-hidden="true"
                                    >
                                        {group.label.toUpperCase()}
                                    </p>
                                )}
                                {group.options.map((opt) => {
                                    runningIndex += 1;
                                    const idx = runningIndex;
                                    const active = idx === activeIndex;
                                    return (
                                        <div
                                            key={opt.id}
                                            id={optionId(idx)}
                                            role="option"
                                            aria-selected={active}
                                            className={cn(
                                                'px-4 py-2 cursor-pointer text-sm flex gap-3',
                                                active
                                                    ? 'bg-brand/10'
                                                    : 'hover:bg-gray-50',
                                                opt.kind === 'all' &&
                                                    'sticky bottom-0 bg-white border-t border-gray-100 text-brand font-medium'
                                            )}
                                            onMouseEnter={() =>
                                                setActiveIndex(idx)
                                            }
                                            onClick={() => activate(opt)}
                                        >
                                            {opt.kind === 'all' && (
                                                <>
                                                    <span className="flex-1">
                                                        See all {total} results
                                                        for &lsquo;
                                                        {value.trim()}&rsquo;
                                                    </span>
                                                    <kbd className="rounded border border-gray-200 px-1.5 text-xs text-gray-500">
                                                        Enter
                                                    </kbd>
                                                </>
                                            )}
                                            {opt.kind === 'result' && (
                                                <>
                                                    <ResultIcon
                                                        item={opt.item}
                                                    />
                                                    <div className="min-w-0 flex-1">
                                                        <div className="flex items-center gap-2">
                                                            <span className="font-medium text-gray-900 truncate">
                                                                {resultTitle(
                                                                    opt.item
                                                                )}
                                                            </span>
                                                            {opt.item.match ===
                                                                'title' &&
                                                                opt.item
                                                                    .content_type ===
                                                                    'library' && (
                                                                    <span className="text-xs text-gray-500 shrink-0">
                                                                        Title
                                                                        match
                                                                    </span>
                                                                )}
                                                        </div>
                                                        {opt.item.match ===
                                                            'content' && (
                                                            <>
                                                                {opt.item
                                                                    .description && (
                                                                    <p className="text-gray-600 line-clamp-2">
                                                                        <HighlightedSnippet
                                                                            text={
                                                                                opt
                                                                                    .item
                                                                                    .description
                                                                            }
                                                                            wordLimit={
                                                                                20
                                                                            }
                                                                        />
                                                                    </p>
                                                                )}
                                                                <p className="text-xs text-gray-500">
                                                                    in{' '}
                                                                    {decodeHtmlEntities(
                                                                        opt.item
                                                                            .title
                                                                    )}
                                                                </p>
                                                            </>
                                                        )}
                                                    </div>
                                                </>
                                            )}
                                        </div>
                                    );
                                })}
                            </div>
                        ))}
                    </div>
                    {groups.length === 0 && !settled && !error && (
                        <div className="px-4 py-3 space-y-3" aria-hidden="true">
                            {[0, 1, 2].map((i) => (
                                <div key={i} className="flex gap-3">
                                    <Skeleton className="size-4 shrink-0" />
                                    <div className="flex-1 space-y-1.5">
                                        <Skeleton className="h-4 w-2/3" />
                                        <Skeleton className="h-3 w-1/2" />
                                    </div>
                                </div>
                            ))}
                        </div>
                    )}
                    {groups.length === 0 && !settled && !error && (
                        <p className="sr-only" role="status">
                            Searching...
                        </p>
                    )}
                    {groups.length === 0 && !!error && (
                        <p
                            className="px-4 py-4 text-sm text-gray-600"
                            role="status"
                        >
                            Search is not available right now. Press Enter to
                            try again.
                        </p>
                    )}
                    {groups.length === 0 && settled && !error && (
                        <div
                            className="flex items-start gap-3 px-4 py-4 text-sm"
                            role="status"
                        >
                            <Search
                                className="size-4 mt-0.5 shrink-0 text-gray-400"
                                aria-hidden="true"
                            />
                            <div>
                                <p className="font-medium text-gray-900">
                                    No results for &lsquo;{value.trim()}&rsquo;
                                </p>
                                <p className="text-gray-500">
                                    Try different words, or press Enter to
                                    search again.
                                </p>
                            </div>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
