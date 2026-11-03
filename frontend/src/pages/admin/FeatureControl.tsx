import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { toast } from 'sonner';
import { AlertCircle, Search } from 'lucide-react';
import { useAuth, fetchUser } from '@/auth/useAuth';
import {
    FeatureAccess,
    FacilityFeatureOverviewRow,
    FacilityFeatureDetailRow,
    FeatureCatalogEntry,
    ServerResponseMany
} from '@/types';
import API from '@/api/api';
import { PageHeader } from '@/components/shared/PageHeader';
import { Switch } from '@/components/ui/switch';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
    Select,
    SelectContent,
    SelectGroup,
    SelectItem,
    SelectLabel,
    SelectSeparator,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';

// The line above each group of sub-features, keyed by their parent: when the
// group applies, and for Programs why it defaults on, so admins read turning it
// off as a deliberate setup step rather than a suggested one. A group whose
// parent is off stays visible but disabled, so admins can see which
// sub-features exist.
const SUB_FEATURE_NOTES: Partial<Record<string, string>> = {
    [FeatureAccess.OpenContentAccess]:
        'Sub-features (only available when Knowledge Center is enabled)',
    [FeatureAccess.ProgramAccess]:
        'Residents can see their own program information by default (only available when Program Hub & Tracking is enabled).'
};

type FilterValue = 'all' | `${string}:true` | `${string}:false`;

function overviewUrl(search: string, filter: FilterValue): string {
    const params = new URLSearchParams();
    if (search) params.set('search', search);
    if (filter !== 'all') {
        const [feature, enabled] = filter.split(':');
        params.set('feature', feature);
        params.set('enabled', enabled);
    }
    const qs = params.toString();
    return `/api/facilities/features${qs ? `?${qs}` : ''}`;
}

function onCount(
    row: FacilityFeatureOverviewRow,
    topLevel: FeatureCatalogEntry[]
): number {
    return topLevel.filter((f) => row.features[f.name]).length;
}

function FeaturePill({ label, on }: { label: string; on: boolean }) {
    return (
        <span
            className={cn(
                'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
                on
                    ? 'bg-green-50 text-green-700 border-green-200 dark:bg-green-950 dark:text-green-400 dark:border-green-800'
                    : 'bg-muted text-muted-foreground border-border'
            )}
        >
            {label}
        </span>
    );
}

function SubFeatureRow({
    sub,
    enabled,
    disabled,
    onToggle
}: {
    sub: FeatureCatalogEntry;
    enabled: boolean;
    disabled: boolean;
    onToggle: () => void;
}) {
    return (
        <div
            className={cn(
                'flex items-start justify-between',
                disabled && 'opacity-50'
            )}
        >
            <div className="flex-1 pr-4">
                <h4 className="text-sm font-medium text-brand-dark dark:text-white">
                    {sub.title}
                </h4>
                {sub.description && (
                    <p className="text-xs text-muted-foreground mt-1">
                        {sub.description}
                    </p>
                )}
            </div>
            <Switch
                aria-label={`Toggle ${sub.title}`}
                checked={enabled}
                disabled={disabled}
                onCheckedChange={onToggle}
            />
        </div>
    );
}

export default function FeatureControl() {
    const { user, setUser } = useAuth();
    const [search, setSearch] = useState('');
    const [debouncedSearch, setDebouncedSearch] = useState('');
    const [filter, setFilter] = useState<FilterValue>('all');
    const [selectedFacilityId, setSelectedFacilityId] = useState<number | null>(
        null
    );
    const [confirmApplyAll, setConfirmApplyAll] = useState(false);
    const [applying, setApplying] = useState(false);
    const [pendingFeature, setPendingFeature] = useState<string | null>(null);

    useEffect(() => {
        const timer = setTimeout(() => setDebouncedSearch(search), 300);
        return () => clearTimeout(timer);
    }, [search]);

    // Every feature, including any a product registered, so a new one shows up
    // here without a frontend change.
    const {
        data: catalogResp,
        isLoading: catalogLoading,
        error: catalogError
    } = useSWR<ServerResponseMany<FeatureCatalogEntry>, Error>('/api/features');
    const catalog = useMemo(() => catalogResp?.data ?? [], [catalogResp]);
    const topLevel = useMemo(() => catalog.filter((f) => !f.parent), [catalog]);

    const {
        data: overviewResp,
        isLoading: overviewLoading,
        mutate: mutateOverview
    } = useSWR<ServerResponseMany<FacilityFeatureOverviewRow>>(
        overviewUrl(debouncedSearch, filter)
    );
    const rows = useMemo(() => overviewResp?.data ?? [], [overviewResp]);

    const {
        data: detailResp,
        isLoading: detailLoading,
        mutate: mutateDetail
    } = useSWR<ServerResponseMany<FacilityFeatureDetailRow>>(
        selectedFacilityId
            ? `/api/facilities/${selectedFacilityId}/features`
            : null
    );
    const detailRows = detailResp?.data ?? [];
    const isEnabled = (feature: string) =>
        detailRows.find((r) => r.feature === feature)?.enabled ?? false;

    // Default to the acting admin's own facility once the list loads.
    useEffect(() => {
        if (selectedFacilityId !== null || rows.length === 0) return;
        const home = user
            ? rows.find((r) => r.facility_id === user.facility_id)
            : undefined;
        setSelectedFacilityId((home ?? rows[0]).facility_id);
    }, [rows, selectedFacilityId, user]);

    const selectedFacility = useMemo(
        () => rows.find((r) => r.facility_id === selectedFacilityId) ?? null,
        [rows, selectedFacilityId]
    );

    if (!user) return null;

    if (catalogError) {
        return (
            <div className="full-page-center">
                <div className="text-red-600">
                    Error loading the feature list
                </div>
            </div>
        );
    }

    async function refreshOwnClaimsIfAffected(facilityId: number) {
        if (user?.facility_id === facilityId) {
            const updated = await fetchUser();
            if (updated) setUser(updated);
        }
    }

    async function handleToggle(feature: string, currentValue: boolean) {
        if (selectedFacilityId === null || pendingFeature !== null || applying)
            return;
        const label = catalog.find((f) => f.name === feature)?.title ?? feature;
        setPendingFeature(feature);
        try {
            const resp = await API.put<string, { enabled: boolean }>(
                `facilities/${selectedFacilityId}/features/${feature}`,
                { enabled: !currentValue }
            );
            if (!resp.success) {
                toast.error(resp.message || `Failed to update ${label}`);
                return;
            }
            toast.success(`${label} ${!currentValue ? 'enabled' : 'disabled'}`);
            await mutateDetail();
            await mutateOverview();
            await refreshOwnClaimsIfAffected(selectedFacilityId);
        } finally {
            setPendingFeature(null);
        }
    }

    // Propagating while a toggle is still in flight would copy the source's
    // pre-toggle state to every other facility, silently leaving the source out
    // of step with the facilities the admin just "applied" it to.
    async function handleApplyAll() {
        if (!selectedFacilityId || pendingFeature !== null) return;
        setApplying(true);
        try {
            const resp = await API.put<string, { source_facility_id: number }>(
                'facilities/features/apply-all',
                { source_facility_id: selectedFacilityId }
            );
            if (!resp.success) {
                toast.error(resp.message || 'Failed to apply settings');
                return;
            }
            toast.success(
                `${selectedFacility?.facility_name ?? 'This facility'}'s settings were applied to all facilities`
            );
            setConfirmApplyAll(false);
            await mutateOverview();
            await mutateDetail();
            const updated = await fetchUser();
            if (updated) setUser(updated);
        } catch {
            toast.error('Failed to apply settings');
        } finally {
            setApplying(false);
        }
    }

    return (
        <div className="p-6 space-y-6">
            <PageHeader
                title="Feature Control"
                subtitle="Turn features on or off for each facility"
            />

            <div className="flex items-start gap-6">
                {/* Left: search + filter + facility list */}
                <div className="w-80 shrink-0 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-[#262626] dark:bg-[#171717]">
                    <div className="space-y-2 border-b border-gray-100 p-3 dark:border-[#262626]">
                        <div className="relative">
                            <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-gray-400" />
                            <Input
                                placeholder="Search facilities..."
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                className="h-8 pl-8 text-sm"
                            />
                        </div>
                        <Select
                            value={filter}
                            onValueChange={(v) => setFilter(v as FilterValue)}
                        >
                            <SelectTrigger className="h-8 text-sm">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">
                                    All facilities
                                </SelectItem>
                                {topLevel.map((feature) => (
                                    <SelectGroup key={feature.name}>
                                        <SelectSeparator />
                                        <SelectLabel>
                                            {feature.title}
                                        </SelectLabel>
                                        <SelectItem
                                            value={`${feature.name}:true`}
                                        >
                                            {feature.title}: On
                                        </SelectItem>
                                        <SelectItem
                                            value={`${feature.name}:false`}
                                        >
                                            {feature.title}: Off
                                        </SelectItem>
                                    </SelectGroup>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="max-h-[calc(100vh-20rem)] overflow-y-auto p-2">
                        {overviewLoading || catalogLoading ? (
                            <p className="px-2 py-3 text-xs text-gray-400">
                                Loading facilities…
                            </p>
                        ) : rows.length === 0 ? (
                            <p className="px-2 py-3 text-xs text-gray-400">
                                No facilities found
                            </p>
                        ) : (
                            rows.map((row) => {
                                const active =
                                    row.facility_id === selectedFacilityId;
                                return (
                                    <button
                                        key={row.facility_id}
                                        type="button"
                                        aria-pressed={active}
                                        onClick={() =>
                                            setSelectedFacilityId(
                                                row.facility_id
                                            )
                                        }
                                        className={cn(
                                            'mb-1 w-full rounded-md px-3 py-2 text-left transition-colors',
                                            active
                                                ? 'border border-brand bg-brand/5'
                                                : 'border border-transparent hover:bg-gray-50 dark:hover:bg-[#262626]'
                                        )}
                                    >
                                        <div className="flex items-center justify-between">
                                            <span className="text-sm font-medium text-brand-dark dark:text-white">
                                                {row.facility_name}
                                            </span>
                                            <span className="text-xs text-gray-500 dark:text-gray-400">
                                                {onCount(row, topLevel)} of{' '}
                                                {topLevel.length} on
                                            </span>
                                        </div>
                                        <div className="mt-1.5 flex flex-wrap gap-1">
                                            {topLevel.map((f) => (
                                                <FeaturePill
                                                    key={f.name}
                                                    label={f.title}
                                                    on={!!row.features[f.name]}
                                                />
                                            ))}
                                        </div>
                                    </button>
                                );
                            })
                        )}
                    </div>
                </div>

                {/* Right: selected facility's feature cards */}
                <div className="min-w-0 flex-1 space-y-4">
                    {selectedFacility ? (
                        <>
                            <div className="flex items-start justify-between">
                                <div>
                                    <h2 className="text-brand-dark dark:text-white">
                                        {selectedFacility.facility_name}
                                    </h2>
                                    <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                                        Changes apply to this facility only
                                    </p>
                                </div>
                                <Button
                                    variant="outline"
                                    onClick={() => setConfirmApplyAll(true)}
                                    disabled={
                                        pendingFeature !== null ||
                                        applying ||
                                        catalogLoading
                                    }
                                >
                                    Apply these settings to all facilities
                                </Button>
                            </div>

                            {catalogLoading && (
                                <p className="text-sm text-gray-500 dark:text-gray-400">
                                    Loading features…
                                </p>
                            )}
                            <div className="space-y-4">
                                {topLevel.map((card) => {
                                    const enabled = isEnabled(card.name);
                                    const subs = catalog.filter(
                                        (f) => f.parent === card.name
                                    );
                                    const note = SUB_FEATURE_NOTES[card.name];
                                    return (
                                        <div
                                            key={card.name}
                                            className="bg-background border border-border rounded-lg p-6"
                                        >
                                            <div className="flex items-start justify-between mb-3">
                                                <div className="flex-1">
                                                    <h3 className="text-lg font-semibold text-brand-dark dark:text-white">
                                                        {card.title}
                                                    </h3>
                                                    {card.description && (
                                                        <p className="text-sm text-muted-foreground mt-1">
                                                            {card.description}
                                                        </p>
                                                    )}
                                                </div>
                                                <Switch
                                                    aria-label={`Toggle ${card.title}`}
                                                    checked={enabled}
                                                    disabled={
                                                        pendingFeature !==
                                                            null ||
                                                        detailLoading ||
                                                        applying
                                                    }
                                                    onCheckedChange={() =>
                                                        void handleToggle(
                                                            card.name,
                                                            enabled
                                                        )
                                                    }
                                                />
                                            </div>

                                            {subs.length > 0 && (
                                                <div className="mt-4 bg-surface-hover rounded-md p-4">
                                                    {note && (
                                                        <div className="flex items-start gap-2 mb-4 text-sm text-muted-foreground">
                                                            <AlertCircle className="size-4 shrink-0" />
                                                            <span>{note}</span>
                                                        </div>
                                                    )}
                                                    <div className="space-y-4">
                                                        {subs.map((sub) => {
                                                            const subEnabled =
                                                                isEnabled(
                                                                    sub.name
                                                                );
                                                            return (
                                                                <SubFeatureRow
                                                                    key={
                                                                        sub.name
                                                                    }
                                                                    sub={sub}
                                                                    enabled={
                                                                        subEnabled
                                                                    }
                                                                    disabled={
                                                                        !enabled ||
                                                                        pendingFeature !==
                                                                            null ||
                                                                        detailLoading ||
                                                                        applying
                                                                    }
                                                                    onToggle={() =>
                                                                        void handleToggle(
                                                                            sub.name,
                                                                            subEnabled
                                                                        )
                                                                    }
                                                                />
                                                            );
                                                        })}
                                                    </div>
                                                </div>
                                            )}
                                        </div>
                                    );
                                })}
                            </div>
                        </>
                    ) : (
                        <p className="text-sm text-gray-500 dark:text-gray-400">
                            Select a facility to manage its features.
                        </p>
                    )}
                </div>
            </div>

            <Dialog open={confirmApplyAll} onOpenChange={setConfirmApplyAll}>
                <DialogContent className="max-w-md">
                    <DialogHeader>
                        <DialogTitle>Apply to all facilities?</DialogTitle>
                        <DialogDescription>
                            This will overwrite every other facility&apos;s
                            feature settings — including sub-features — with{' '}
                            {selectedFacility?.facility_name ?? 'this facility'}
                            &apos;s current settings. This cannot be undone.
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button
                            variant="outline"
                            onClick={() => setConfirmApplyAll(false)}
                            disabled={applying}
                        >
                            Cancel
                        </Button>
                        <Button
                            onClick={() => void handleApplyAll()}
                            disabled={applying || pendingFeature !== null}
                            className="bg-brand hover:bg-brand-dark text-white"
                        >
                            {applying ? 'Applying…' : 'Apply to All Facilities'}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
