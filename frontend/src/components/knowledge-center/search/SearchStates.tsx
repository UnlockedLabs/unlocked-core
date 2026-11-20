import { ReactNode } from 'react';
import { AlertCircle, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';

/**
 * Icon + message block for the results page, using the app's `empty-state`
 * card (as the Knowledge Center grid and the provider-matching sections do).
 */
export function SearchMessage({
    icon,
    title,
    hint,
    action
}: {
    icon: ReactNode;
    title: string;
    hint?: string;
    action?: ReactNode;
}) {
    return (
        <div className="empty-state px-6">
            <div className="mx-auto mb-3 flex size-10 items-center justify-center rounded-lg bg-brand/10">
                {icon}
            </div>
            <p className="font-medium text-gray-900">{title}</p>
            {hint && <p className="mt-1 text-sm text-gray-500">{hint}</p>}
            {action && <div className="mt-4">{action}</div>}
        </div>
    );
}

export function SearchPrompt() {
    return (
        <SearchMessage
            icon={<Search className="size-5 text-brand" aria-hidden="true" />}
            title="Search the Knowledge Center"
            hint="Type a word above to search library titles and everything inside libraries."
        />
    );
}

export function NoResults({ query }: { query: string }) {
    return (
        <SearchMessage
            icon={<Search className="size-5 text-brand" aria-hidden="true" />}
            title={`No results for ‘${query}’`}
            hint="Check the spelling, try different or fewer words, or clear a filter."
        />
    );
}

export function SearchError({ onRetry }: { onRetry: () => void }) {
    return (
        <SearchMessage
            icon={
                <AlertCircle
                    className="size-5 text-brand-gold"
                    aria-hidden="true"
                />
            }
            title="Search is not available right now"
            hint="Something went wrong on our side. Please try again."
            action={
                <Button type="button" variant="outline" onClick={onRetry}>
                    Try again
                </Button>
            }
        />
    );
}

/** Placeholder result cards, shaped like SearchResultCard. */
export function ResultsSkeleton({ count = 4 }: { count?: number }) {
    return (
        <ul className="space-y-3" aria-hidden="true">
            {Array.from({ length: count }, (_, i) => (
                <li key={i} className="card-block p-4 flex gap-4">
                    <Skeleton className="size-10 shrink-0 rounded-lg" />
                    <div className="flex-1 space-y-2">
                        <Skeleton className="h-3 w-24" />
                        <Skeleton className="h-5 w-2/3" />
                        <Skeleton className="h-4 w-full" />
                        <Skeleton className="h-4 w-3/4" />
                    </div>
                </li>
            ))}
        </ul>
    );
}
