import { useState, useMemo, useEffect, useRef, ReactNode } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useUrlPagination } from '@/hooks/useUrlPagination';
import { useCanvasLoadingPoll } from '@/hooks/useCanvasLoadingPoll';
import { useNavigate } from 'react-router-dom';
import useSWR from 'swr';
import { useAuth, canSwitchFacility } from '@/auth/useAuth';
import {
    ProgramsOverviewTable,
    ProgramType,
    ProgramEffectiveStatus,
    ServerResponseMany,
    ServerResponseOne,
    CreditType,
    FundingType,
    Program,
    ProgramCreditType,
    PgmType,
    Facility,
    isExternalSource,
    externalSourceLabel
} from '@/types';
import API from '@/api/api';
import { toast } from 'sonner';
import { NoDataDash } from '@/components/shared';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage
} from '@/components/ui/form';
import { programCreateSchema, ProgramCreateInput } from '@/lib/validation';
import { Card, CardContent } from '@/components/ui/card';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import {
    Popover,
    PopoverContent,
    PopoverTrigger
} from '@/components/ui/popover';
import { Checkbox } from '@/components/ui/checkbox';
import { Search, Plus, Filter, ChevronDown, Loader2, X } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { Badge } from '@/components/ui/badge';
import { Pagination } from '@/components/Pagination';
import {
    NumericFilterPanel,
    type NumericFilterField,
    type NumericFilterValue
} from '@/components/shared/NumericFilterPanel';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow
} from '@/components/ui/table';
import {
    Tooltip,
    TooltipContent,
    TooltipTrigger,
    TooltipProvider
} from '@/components/ui/tooltip';
import {
    programTypeColors,
    statusColors,
    type SortOption
} from '@/pages/program-detail/constants';
import { clickableProps } from '@/lib/a11y';

function getEffectiveStatus(
    program: ProgramsOverviewTable
): ProgramEffectiveStatus {
    if (program.archived_at) return ProgramEffectiveStatus.Archived;
    if (program.status) return ProgramEffectiveStatus.Available;
    return ProgramEffectiveStatus.Inactive;
}

function formatDisplayName(value: string): string {
    return value.replace(/_/g, ' ').replace(/-/g, ' ');
}

function parseCommaSeparated(value: string | null | undefined): string[] {
    if (!value) return [];
    return value
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
}

function FilterChip({
    label,
    onRemove
}: {
    label: string;
    onRemove: () => void;
}) {
    return (
        <Badge variant="secondary" className="gap-1 pr-1 font-normal">
            <span>{label}</span>
            <button
                type="button"
                aria-label={`Remove ${label} filter`}
                onClick={onRemove}
                className="rounded-full p-0.5 hover:bg-gray-200"
            >
                <X className="size-3.5" aria-hidden />
            </button>
        </Badge>
    );
}

// Keys map straight to the backend's `filter_<key>` query param names
// (see the `for col, val := range filters` switch in
// backend/src/database/programs.go) so building the fetch URL is a direct
// lookup, not a translation layer.
type NumericFilterKey =
    | 'mr.total_active_classes'
    | 'mr.total_active_enrollments'
    | 'capacity_utilization'
    | 'completion_rate'
    | 'attendance_rate'
    | 'mr.total_active_facilities';

const BASE_NUMERIC_FILTER_FIELDS: NumericFilterField<NumericFilterKey>[] = [
    { key: 'mr.total_active_classes', label: 'Classes' },
    { key: 'mr.total_active_enrollments', label: 'Enrollment' },
    { key: 'capacity_utilization', label: 'Capacity', suffix: '%' },
    { key: 'completion_rate', label: 'Completion', suffix: '%' },
    { key: 'attendance_rate', label: 'Attendance', suffix: '%' }
];

// The backend only applies `mr.total_active_facilities` when the request is
// NOT facility-scoped (see `if !facilityScoped` in programs.go) — showing it
// to a facility admin would be a filter that silently does nothing.
const DEPT_ADMIN_NUMERIC_FILTER_FIELDS: NumericFilterField<NumericFilterKey>[] =
    [
        ...BASE_NUMERIC_FILTER_FIELDS,
        { key: 'mr.total_active_facilities', label: 'Facilities' }
    ];

const SORT_ORDER_BY: Record<SortOption, [string, string]> = {
    'name-asc': ['programs.name', 'asc'],
    'name-desc': ['programs.name', 'desc'],
    'enrollment-asc': ['mr.total_active_enrollments', 'asc'],
    'enrollment-desc': ['mr.total_active_enrollments', 'desc'],
    'completion-asc': ['completion_rate', 'asc'],
    'completion-desc': ['completion_rate', 'desc']
};

function numericFilterQueryParams(
    numericFilters: Partial<Record<NumericFilterKey, NumericFilterValue>>
): [string, string][] {
    return (
        Object.entries(numericFilters) as [
            NumericFilterKey,
            NumericFilterValue | undefined
        ][]
    )
        .filter((entry): entry is [NumericFilterKey, NumericFilterValue] =>
            Boolean(entry[1])
        )
        .map(([key, filter]) => [
            `filter_${key}`,
            `${filter.operator}${filter.value}`
        ]);
}

export default function ProgramsPage() {
    const { user } = useAuth();
    const navigate = useNavigate();
    const isDeptAdminUser = user ? canSwitchFacility(user) : false;

    const numericFilterFields = isDeptAdminUser
        ? DEPT_ADMIN_NUMERIC_FILTER_FIELDS
        : BASE_NUMERIC_FILTER_FIELDS;

    const { page, perPage, setPage, setPerPage } = useUrlPagination(1, 20);

    const [search, setSearch] = useState('');
    const [sort, setSort] = useState<SortOption>('name-asc');
    const [selectedTypes, setSelectedTypes] = useState<ProgramType[]>([]);
    const [selectedStatuses, setSelectedStatuses] = useState<
        ProgramEffectiveStatus[]
    >([]);
    const [numericFilters, setNumericFilters] = useState<
        Partial<Record<NumericFilterKey, NumericFilterValue>>
    >({});

    // Filtering, sorting, and pagination all happen server-side (see the
    // `filter_*`/`order_by`/`order`/`page`/`per_page` params GORM applies in
    // GetProgramsOverviewTable) so this table scales past a single page of
    // results instead of needing every program loaded into the browser.
    const programsQuery = useMemo(() => {
        const params = new URLSearchParams({
            include_archived: 'true',
            page: String(page),
            per_page: String(perPage)
        });
        if (search) params.set('search', search);
        const [orderBy, order] = SORT_ORDER_BY[sort];
        params.set('order_by', orderBy);
        params.set('order', order);
        if (selectedTypes.length > 0) {
            params.set('filter_pt.program_types', selectedTypes.join('|'));
        }
        if (selectedStatuses.length > 0) {
            params.set('filter_programs.is_active', selectedStatuses.join('|'));
        }
        for (const [key, value] of numericFilterQueryParams(numericFilters)) {
            params.set(key, value);
        }
        return `/api/programs/detailed-list?${params.toString()}`;
    }, [
        page,
        perPage,
        search,
        sort,
        selectedTypes,
        selectedStatuses,
        numericFilters
    ]);

    const { data: resp, mutate } =
        useSWR<ServerResponseMany<ProgramsOverviewTable>>(programsQuery);

    const programs = resp?.data ?? [];

    // Facility-wide totals for the stat cards, independent of the table's
    // filters — a KPI header, not a summary of the currently filtered rows.
    const { data: statsResp } = useSWR<
        ServerResponseMany<ProgramsOverviewTable>
    >('/api/programs/detailed-list?include_archived=true&all=true');

    const hasLoadingCanvas = programs.some(
        (p) => isExternalSource(p.source) && p.loading
    );
    const { exhausted: canvasPollExhausted } = useCanvasLoadingPoll(
        hasLoadingCanvas,
        mutate
    );

    // Fetch facilities for Department Admin
    const { data: facilitiesResp } = useSWR<ServerResponseMany<Facility>>(
        isDeptAdminUser ? '/api/facilities' : null
    );
    const facilities = facilitiesResp?.data ?? [];

    const [showAddProgram, setShowAddProgram] = useState(false);
    const emptyProgramForm: ProgramCreateInput = {
        name: '',
        description: '',
        types: [],
        creditTypes: [],
        fundingTypes: [],
        status: ProgramEffectiveStatus.Available,
        hasProgramCompletion: false,
        facilities: []
    };
    const programForm = useForm<ProgramCreateInput>({
        resolver: zodResolver(programCreateSchema),
        defaultValues: emptyProgramForm
    });
    const {
        formState: { isSubmitting }
    } = programForm;

    const handleCreateProgram = async (data: ProgramCreateInput) => {
        try {
            const payload = {
                name: data.name,
                description: data.description,
                program_types: data.types.map(
                    (type): PgmType => ({ program_type: type })
                ),
                credit_types: data.creditTypes.map(
                    (type): ProgramCreditType => ({ credit_type: type })
                ),
                funding_type: data.fundingTypes[0],
                is_active: data.status === ProgramEffectiveStatus.Available,
                has_program_completion: data.hasProgramCompletion,
                facilities: data.facilities
            };

            const resp = (await API.post<Program, typeof payload>(
                'programs',
                payload
            )) as ServerResponseOne<Program>;

            if (!resp.success) {
                toast.error('Failed to create program');
                return;
            }

            toast.success('Program created successfully');

            setShowAddProgram(false);
            programForm.reset(emptyProgramForm);

            navigate(`/programs/${resp.data.id}`);
        } catch {
            toast.error('Failed to create program');
        }
    };

    // Reset to page 1 only when a filter/sort/search value actually changes.
    // Comparing against the previous values (rather than a first-render ref) keeps
    // this correct under React StrictMode, which double-invokes effects on mount
    // and would otherwise defeat the ref guard and reset a deep-linked/refreshed
    // page back to 1.
    const prevFilters = useRef({
        search,
        sort,
        selectedTypes,
        selectedStatuses,
        numericFilters
    });
    useEffect(() => {
        const prev = prevFilters.current;
        if (
            prev.search !== search ||
            prev.sort !== sort ||
            prev.selectedTypes !== selectedTypes ||
            prev.selectedStatuses !== selectedStatuses ||
            prev.numericFilters !== numericFilters
        ) {
            prevFilters.current = {
                search,
                sort,
                selectedTypes,
                selectedStatuses,
                numericFilters
            };
            setPage(1);
        }
    }, [
        search,
        sort,
        selectedTypes,
        selectedStatuses,
        numericFilters,
        setPage
    ]);

    const toggleTypeFilter = (type: ProgramType) => {
        setSelectedTypes((prev) =>
            prev.includes(type)
                ? prev.filter((t) => t !== type)
                : [...prev, type]
        );
    };

    const toggleStatusFilter = (status: ProgramEffectiveStatus) => {
        setSelectedStatuses((prev) =>
            prev.includes(status)
                ? prev.filter((s) => s !== status)
                : [...prev, status]
        );
    };

    const clearFilters = () => {
        setSelectedTypes([]);
        setSelectedStatuses([]);
        setNumericFilters({});
    };

    const onNumericFilterChange = (
        key: NumericFilterKey,
        value: NumericFilterValue | undefined
    ) => {
        setNumericFilters((prev) => {
            const next = { ...prev };
            if (value) {
                next[key] = value;
            } else {
                delete next[key];
            }
            return next;
        });
    };

    const activeFilterCount =
        selectedTypes.length +
        selectedStatuses.length +
        Object.keys(numericFilters).length;

    const programTypes: { value: ProgramType; label: string }[] = [
        { value: ProgramType.EDUCATIONAL, label: 'Educational' },
        { value: ProgramType.VOCATIONAL, label: 'Vocational' },
        { value: ProgramType.MENTAL_HEALTH, label: 'Mental Health' },
        { value: ProgramType.THERAPEUTIC, label: 'Therapeutic' },
        { value: ProgramType.LIFE_SKILLS, label: 'Life Skills' },
        { value: ProgramType.RE_ENTRY, label: 'Re-Entry' },
        { value: ProgramType.RELIGIOUS, label: 'Faith-Based' }
    ];

    const creditTypes: { value: CreditType; label: string }[] = [
        { value: CreditType.COMPLETION, label: 'Completion' },
        { value: CreditType.EARNED_TIME, label: 'Earned Time' },
        { value: CreditType.EDUCATION, label: 'Education' },
        { value: CreditType.PARTICIPATION, label: 'Participation' }
    ];

    const fundingTypes: { value: FundingType; label: string }[] = [
        { value: FundingType.EDUCATIONAL_GRANTS, label: 'Educational Grants' },
        { value: FundingType.FEDERAL_GRANTS, label: 'Federal Grants' },
        { value: FundingType.INMATE_WELFARE, label: 'Inmate Welfare Funds' },
        {
            value: FundingType.NON_PROFIT_ORGANIZATION,
            label: 'Nonprofit Organizations'
        },
        { value: FundingType.STATE_GRANTS, label: 'State Grants' },
        { value: FundingType.OTHER, label: 'Other' }
    ];

    const stats = useMemo(() => {
        const all = statsResp?.data ?? [];
        const active = all.filter((p) => p.status && !p.archived_at);
        const totalEnrollment = all.reduce(
            (sum, p) => sum + (p.total_active_enrollments ?? 0),
            0
        );
        const totalClasses = all.reduce(
            (sum, p) => sum + (p.total_classes ?? 0),
            0
        );

        // Sum the server's own numerator and denominator. Reconstructing them from
        // completion_rate x (total_enrollments - total_active_enrollments) skews the
        // result, because total_enrollments counts enrollments the rate excludes
        // (Scheduled/Cancelled classes, and rows with no enrollment_ended_at).
        const rateEligibleCompletions = all.reduce(
            (sum, p) => sum + (p.rate_eligible_completions ?? 0),
            0
        );
        const rateEligibleEnrollments = all.reduce(
            (sum, p) => sum + (p.rate_eligible_enrollments ?? 0),
            0
        );
        const completionRate =
            rateEligibleEnrollments > 0
                ? Math.round(
                      (rateEligibleCompletions / rateEligibleEnrollments) * 100
                  )
                : null;

        return {
            activePrograms: active.length,
            totalClasses,
            totalEnrollment,
            completionRate
        };
    }, [statsResp]);

    const subtitle = isDeptAdminUser
        ? 'Monitor program performance across all facilities'
        : 'Supporting resident growth and rehabilitation';

    return (
        <div className="min-h-[calc(100vh-4rem)] bg-surface-hover dark:bg-[#0a0a0a]">
            <div className="max-w-7xl mx-auto px-6 pt-[34px] pb-8">
                {/* Header */}
                <div className="mb-8">
                    <div className="flex items-center justify-between mb-2">
                        <div>
                            <h1 className="text-2xl font-medium text-brand-dark">
                                Programs
                            </h1>
                            <p className="text-gray-600 mt-1.5 leading-[1.5]">
                                {subtitle}
                            </p>
                        </div>
                        <Button
                            className="bg-brand-gold text-brand-dark hover:bg-brand-gold-dark gap-2 cursor-default"
                            onClick={() => setShowAddProgram(!showAddProgram)}
                        >
                            <Plus className="size-5" />
                            {showAddProgram
                                ? 'Cancel'
                                : isDeptAdminUser
                                  ? 'Create Statewide Program'
                                  : 'Add Program'}
                        </Button>
                    </div>
                </div>

                {/* Stats Overview */}
                <TooltipProvider>
                    <div className="grid grid-cols-4 gap-6 mb-8">
                        <StatCard
                            label="Active Programs"
                            value={stats.activePrograms}
                            tooltip={
                                isDeptAdminUser
                                    ? 'The count of unique programs offered across all facilities'
                                    : 'The count of unique programs offered at this facility'
                            }
                        />
                        <StatCard
                            label="Total Classes"
                            value={stats.totalClasses}
                            tooltip={
                                isDeptAdminUser
                                    ? 'All classes across all facilities (active, completed, and scheduled)'
                                    : 'All classes at this facility (active, completed, and scheduled)'
                            }
                        />
                        <StatCard
                            label="Total Enrolled"
                            value={stats.totalEnrollment}
                            tooltip={
                                isDeptAdminUser
                                    ? 'The total number of enrollments across all facilities. A single resident can be enrolled in more than one program.'
                                    : 'The total number of enrollments at this facility. A single resident can be enrolled in more than one program.'
                            }
                        />
                        <StatCard
                            label="Completion Rate"
                            value={
                                stats.completionRate !== null ? (
                                    `${stats.completionRate}%`
                                ) : (
                                    <NoDataDash />
                                )
                            }
                            tooltip={
                                stats.completionRate !== null
                                    ? isDeptAdminUser
                                        ? 'The percentage of residents who have completed a class across all facilities'
                                        : 'The percentage of residents who have completed a class at this facility'
                                    : 'No residents have reached completion eligibility yet'
                            }
                        />
                    </div>
                </TooltipProvider>

                {/* Filters */}
                <div className="card-block p-4 mb-6">
                    <div className="flex gap-4">
                        <div className="relative flex-1">
                            <Search className="input-icon-left size-5" />
                            <Input
                                placeholder="Search programs..."
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                className="pl-10 focus-visible:border-gray-400 focus-visible:ring-gray-400/50 dark:!bg-[rgba(38,38,38,0.3)]"
                            />
                        </div>

                        {/* Sort Dropdown */}
                        <Select
                            value={sort}
                            onValueChange={(v) => setSort(v as SortOption)}
                        >
                            <SelectTrigger className="w-55 focus-visible:border-gray-400 focus-visible:ring-gray-400/50 dark:!bg-[rgba(38,38,38,0.3)] cursor-default">
                                <SelectValue placeholder="Sort By" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="name-asc">
                                    Name (A-Z)
                                </SelectItem>
                                <SelectItem value="name-desc">
                                    Name (Z-A)
                                </SelectItem>
                                <SelectItem value="enrollment-asc">
                                    Enrollment (Low-High)
                                </SelectItem>
                                <SelectItem value="enrollment-desc">
                                    Enrollment (High-Low)
                                </SelectItem>
                                <SelectItem value="completion-asc">
                                    Completion (Low-High)
                                </SelectItem>
                                <SelectItem value="completion-desc">
                                    Completion (High-Low)
                                </SelectItem>
                            </SelectContent>
                        </Select>
                        {/* Program Type Filter */}
                        <Popover>
                            <PopoverTrigger asChild>
                                <button className="filter-button">
                                    <div className="flex items-center gap-2">
                                        <Filter className="size-4" />
                                        <span>
                                            Type{' '}
                                            {selectedTypes.length > 0 &&
                                                `(${selectedTypes.length})`}
                                        </span>
                                    </div>
                                    <ChevronDown className="size-4" />
                                </button>
                            </PopoverTrigger>
                            <PopoverContent className="w-60 p-4">
                                <div className="mb-3 font-medium text-sm">
                                    Filter by Program Type
                                </div>
                                <div className="space-y-3">
                                    {Object.values(ProgramType).map((type) => (
                                        <label
                                            key={type}
                                            className="flex items-center gap-2 cursor-pointer"
                                        >
                                            <Checkbox
                                                checked={selectedTypes.includes(
                                                    type
                                                )}
                                                onCheckedChange={() =>
                                                    toggleTypeFilter(type)
                                                }
                                            />
                                            <span className="text-sm">
                                                {formatDisplayName(type)}
                                            </span>
                                        </label>
                                    ))}
                                </div>
                            </PopoverContent>
                        </Popover>
                        {/* Status Filter */}
                        <Popover>
                            <PopoverTrigger asChild>
                                <button className="filter-button">
                                    <div className="flex items-center gap-2">
                                        <Filter className="size-4" />
                                        <span>
                                            Status{' '}
                                            {selectedStatuses.length > 0 &&
                                                `(${selectedStatuses.length})`}
                                        </span>
                                    </div>
                                    <ChevronDown className="size-4" />
                                </button>
                            </PopoverTrigger>
                            <PopoverContent className="w-60 p-4">
                                <div className="mb-3 font-medium text-sm">
                                    Filter by Status
                                </div>
                                <div className="space-y-3 mb-4">
                                    {Object.values(ProgramEffectiveStatus).map(
                                        (status) => (
                                            <label
                                                key={status}
                                                className="flex items-center gap-2 cursor-pointer"
                                            >
                                                <Checkbox
                                                    checked={selectedStatuses.includes(
                                                        status
                                                    )}
                                                    onCheckedChange={() =>
                                                        toggleStatusFilter(
                                                            status
                                                        )
                                                    }
                                                />
                                                <span className="text-sm">
                                                    {status}
                                                </span>
                                            </label>
                                        )
                                    )}
                                </div>
                            </PopoverContent>
                        </Popover>
                        {/* Classes / Enrollment / Capacity / Completion / Attendance Filters */}
                        <NumericFilterPanel
                            label="More Filters"
                            fields={numericFilterFields}
                            values={numericFilters}
                            onChange={onNumericFilterChange}
                        />
                    </div>
                    {activeFilterCount > 0 && (
                        <div className="flex flex-wrap items-center gap-2 mt-3">
                            {selectedTypes.map((type) => (
                                <FilterChip
                                    key={`type-${type}`}
                                    label={formatDisplayName(type)}
                                    onRemove={() => toggleTypeFilter(type)}
                                />
                            ))}
                            {selectedStatuses.map((status) => (
                                <FilterChip
                                    key={`status-${status}`}
                                    label={status}
                                    onRemove={() => toggleStatusFilter(status)}
                                />
                            ))}
                            {(
                                Object.entries(numericFilters) as [
                                    NumericFilterKey,
                                    NumericFilterValue | undefined
                                ][]
                            ).map(([key, filter]) => {
                                if (!filter) return null;
                                const field = numericFilterFields.find(
                                    (f) => f.key === key
                                );
                                if (!field) return null;
                                return (
                                    <FilterChip
                                        key={`numeric-${key}`}
                                        label={`${field.label} ${filter.operator} ${filter.value}${field.suffix ?? ''}`}
                                        onRemove={() =>
                                            onNumericFilterChange(
                                                key,
                                                undefined
                                            )
                                        }
                                    />
                                );
                            })}
                            <button
                                type="button"
                                onClick={clearFilters}
                                className="text-xs text-brand hover:underline ml-1"
                            >
                                Clear all
                            </button>
                        </div>
                    )}
                </div>

                {/* Add Program Form */}
                {showAddProgram && (
                    <div className="card-block p-6 mb-6">
                        <h3 className="text-brand-dark mb-4">
                            {isDeptAdminUser
                                ? 'Create Statewide Program'
                                : 'Add New Program'}
                        </h3>
                        <Form {...programForm}>
                            <form
                                onSubmit={(e) => {
                                    e.preventDefault();
                                    void programForm.handleSubmit(
                                        handleCreateProgram
                                    )(e);
                                }}
                                className="space-y-6"
                            >
                                {/* Basic Information */}
                                <div>
                                    <h4 className="text-sm text-gray-700 mb-3">
                                        Basic Information
                                    </h4>
                                    <div className="grid grid-cols-2 gap-4">
                                        <FormField
                                            control={programForm.control}
                                            name="name"
                                            render={({ field }) => (
                                                <FormItem>
                                                    <FormLabel
                                                        htmlFor="programName"
                                                        className="text-black"
                                                    >
                                                        Program Name *
                                                    </FormLabel>
                                                    <FormControl>
                                                        <Input
                                                            id="programName"
                                                            placeholder="e.g., GED Preparation"
                                                            className="focus-visible:border-gray-400 focus-visible:ring-gray-400/50 dark:!bg-[rgba(38,38,38,0.3)]"
                                                            {...field}
                                                        />
                                                    </FormControl>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />
                                        <FormField
                                            control={programForm.control}
                                            name="description"
                                            render={({ field }) => (
                                                <FormItem className="col-span-2">
                                                    <FormLabel
                                                        htmlFor="programDescription"
                                                        className="text-black"
                                                    >
                                                        Description
                                                    </FormLabel>
                                                    <FormControl>
                                                        <Textarea
                                                            id="programDescription"
                                                            placeholder="Brief description of the program and its goals"
                                                            rows={2}
                                                            className="min-h-0 focus-visible:border-gray-400 focus-visible:ring-gray-400/50 dark:!bg-[rgba(38,38,38,0.3)]"
                                                            {...field}
                                                        />
                                                    </FormControl>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />
                                    </div>
                                </div>

                                {/* Categorization */}
                                <div className="pt-4 border-t border-gray-200">
                                    <h4 className="text-sm text-gray-700 mb-3">
                                        Categorization
                                    </h4>
                                    <div className="grid grid-cols-2 gap-4">
                                        <FormField
                                            control={programForm.control}
                                            name="types"
                                            render={({ field }) => (
                                                <FormItem>
                                                    <FormLabel className="text-black">
                                                        Category (Program Types)
                                                        *
                                                    </FormLabel>
                                                    <div className="mt-2 space-y-2">
                                                        {programTypes.map(
                                                            (type) => (
                                                                <label
                                                                    key={
                                                                        type.value
                                                                    }
                                                                    className="flex items-center gap-2 cursor-pointer"
                                                                >
                                                                    <span className="inline-flex rounded-[1px] focus-within:ring-2 focus-within:ring-gray-300/80 focus-within:ring-offset-1 focus-within:ring-offset-white">
                                                                        <input
                                                                            type="checkbox"
                                                                            checked={field.value.includes(
                                                                                type.value
                                                                            )}
                                                                            onChange={(
                                                                                e
                                                                            ) =>
                                                                                field.onChange(
                                                                                    e
                                                                                        .target
                                                                                        .checked
                                                                                        ? [
                                                                                              ...field.value,
                                                                                              type.value
                                                                                          ]
                                                                                        : field.value.filter(
                                                                                              (
                                                                                                  t
                                                                                              ) =>
                                                                                                  t !==
                                                                                                  type.value
                                                                                          )
                                                                                )
                                                                            }
                                                                            className="checkbox-brand"
                                                                        />
                                                                    </span>
                                                                    <span className="text-sm text-gray-700">
                                                                        {
                                                                            type.label
                                                                        }
                                                                    </span>
                                                                </label>
                                                            )
                                                        )}
                                                    </div>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                        <FormField
                                            control={programForm.control}
                                            name="creditTypes"
                                            render={({ field }) => (
                                                <FormItem>
                                                    <FormLabel className="text-black">
                                                        Credit Types *
                                                    </FormLabel>
                                                    <div className="mt-2 space-y-2">
                                                        {creditTypes.map(
                                                            (type) => (
                                                                <label
                                                                    key={
                                                                        type.value
                                                                    }
                                                                    className="flex items-center gap-2 cursor-pointer"
                                                                >
                                                                    <span className="inline-flex rounded-[1px] focus-within:ring-2 focus-within:ring-gray-300/80 focus-within:ring-offset-1 focus-within:ring-offset-white">
                                                                        <input
                                                                            type="checkbox"
                                                                            checked={field.value.includes(
                                                                                type.value
                                                                            )}
                                                                            onChange={(
                                                                                e
                                                                            ) =>
                                                                                field.onChange(
                                                                                    e
                                                                                        .target
                                                                                        .checked
                                                                                        ? [
                                                                                              ...field.value,
                                                                                              type.value
                                                                                          ]
                                                                                        : field.value.filter(
                                                                                              (
                                                                                                  t
                                                                                              ) =>
                                                                                                  t !==
                                                                                                  type.value
                                                                                          )
                                                                                )
                                                                            }
                                                                            className="checkbox-brand"
                                                                        />
                                                                    </span>
                                                                    <span className="text-sm text-gray-700">
                                                                        {
                                                                            type.label
                                                                        }
                                                                    </span>
                                                                </label>
                                                            )
                                                        )}
                                                    </div>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                        <FormField
                                            control={programForm.control}
                                            name="fundingTypes"
                                            render={({ field }) => (
                                                <FormItem className="col-span-2">
                                                    <FormLabel
                                                        htmlFor="fundingType"
                                                        className="text-black"
                                                    >
                                                        Funding Type *
                                                    </FormLabel>
                                                    <Select
                                                        value={
                                                            field.value[0] ?? ''
                                                        }
                                                        onValueChange={(
                                                            value
                                                        ) =>
                                                            field.onChange([
                                                                value as FundingType
                                                            ])
                                                        }
                                                    >
                                                        <FormControl>
                                                            <SelectTrigger
                                                                id="fundingType"
                                                                className="focus-visible:border-gray-400 focus-visible:ring-gray-400/50 dark:!bg-[rgba(38,38,38,0.3)]"
                                                            >
                                                                <SelectValue placeholder="Select funding type" />
                                                            </SelectTrigger>
                                                        </FormControl>
                                                        <SelectContent>
                                                            {fundingTypes.map(
                                                                (type) => (
                                                                    <SelectItem
                                                                        key={
                                                                            type.value
                                                                        }
                                                                        value={
                                                                            type.value
                                                                        }
                                                                    >
                                                                        {
                                                                            type.label
                                                                        }
                                                                    </SelectItem>
                                                                )
                                                            )}
                                                        </SelectContent>
                                                    </Select>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                        <FormField
                                            control={programForm.control}
                                            name="status"
                                            render={({ field }) => (
                                                <FormItem className="col-span-2">
                                                    <FormLabel
                                                        htmlFor="programStatus"
                                                        className="text-black"
                                                    >
                                                        Program Availability *
                                                    </FormLabel>
                                                    <Select
                                                        value={field.value}
                                                        onValueChange={
                                                            field.onChange
                                                        }
                                                    >
                                                        <FormControl>
                                                            <SelectTrigger
                                                                id="programStatus"
                                                                className="focus-visible:border-gray-400 focus-visible:ring-gray-400/50 dark:!bg-[rgba(38,38,38,0.3)]"
                                                            >
                                                                <SelectValue placeholder="Select program status" />
                                                            </SelectTrigger>
                                                        </FormControl>
                                                        <SelectContent>
                                                            <SelectItem
                                                                value={
                                                                    ProgramEffectiveStatus.Available
                                                                }
                                                            >
                                                                Available
                                                            </SelectItem>
                                                            <SelectItem
                                                                value={
                                                                    ProgramEffectiveStatus.Inactive
                                                                }
                                                            >
                                                                Inactive
                                                            </SelectItem>
                                                            <SelectItem
                                                                value={
                                                                    ProgramEffectiveStatus.Archived
                                                                }
                                                            >
                                                                Archived
                                                            </SelectItem>
                                                        </SelectContent>
                                                    </Select>
                                                    <p className="text-xs text-gray-500 mt-1">
                                                        Available programs can
                                                        accept new class
                                                        enrollments. Inactive
                                                        programs are temporarily
                                                        paused. Archived
                                                        programs are no longer
                                                        offered.
                                                    </p>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                        <FormField
                                            control={programForm.control}
                                            name="hasProgramCompletion"
                                            render={({ field }) => (
                                                <FormItem className="col-span-2">
                                                    <FormLabel className="text-black">
                                                        Program Completion
                                                    </FormLabel>
                                                    <div className="flex items-start gap-2">
                                                        <FormControl>
                                                            <Checkbox
                                                                id="hasProgramCompletion"
                                                                checked={
                                                                    field.value
                                                                }
                                                                onCheckedChange={(
                                                                    checked
                                                                ) =>
                                                                    field.onChange(
                                                                        checked ===
                                                                            true
                                                                    )
                                                                }
                                                                className="mt-0.5"
                                                            />
                                                        </FormControl>
                                                        <label
                                                            htmlFor="hasProgramCompletion"
                                                            className="text-sm leading-snug"
                                                        >
                                                            Residents must
                                                            complete all classes
                                                            to complete this
                                                            program
                                                        </label>
                                                    </div>
                                                    <p className="text-xs text-gray-500 mt-1">
                                                        Leave unchecked when
                                                        each class stands on its
                                                        own. Applies to every
                                                        facility offering this
                                                        program.
                                                    </p>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />

                                        {/* Facilities Selection - Only for Department Admin */}
                                        {isDeptAdminUser && (
                                            <FormField
                                                control={programForm.control}
                                                name="facilities"
                                                render={({ field }) => (
                                                    <FormItem className="col-span-2">
                                                        <FormLabel className="text-black">
                                                            Facilities Offered *
                                                        </FormLabel>
                                                        <div className="mt-2 max-h-60 overflow-y-auto space-y-2 p-3">
                                                            {facilities.length ===
                                                            0 ? (
                                                                <p className="text-sm text-gray-500">
                                                                    Loading
                                                                    facilities...
                                                                </p>
                                                            ) : (
                                                                facilities.map(
                                                                    (
                                                                        facility
                                                                    ) => (
                                                                        <label
                                                                            key={
                                                                                facility.id
                                                                            }
                                                                            className="flex items-center gap-2 cursor-pointer"
                                                                        >
                                                                            <span className="inline-flex rounded-[1px] focus-within:ring-2 focus-within:ring-gray-300/80 focus-within:ring-offset-1 focus-within:ring-offset-white">
                                                                                <input
                                                                                    type="checkbox"
                                                                                    checked={field.value.includes(
                                                                                        facility.id
                                                                                    )}
                                                                                    onChange={(
                                                                                        e
                                                                                    ) =>
                                                                                        field.onChange(
                                                                                            e
                                                                                                .target
                                                                                                .checked
                                                                                                ? [
                                                                                                      ...field.value,
                                                                                                      facility.id
                                                                                                  ]
                                                                                                : field.value.filter(
                                                                                                      (
                                                                                                          f
                                                                                                      ) =>
                                                                                                          f !==
                                                                                                          facility.id
                                                                                                  )
                                                                                        )
                                                                                    }
                                                                                    className="checkbox-brand"
                                                                                />
                                                                            </span>
                                                                            <span className="text-sm text-gray-700">
                                                                                {
                                                                                    facility.name
                                                                                }
                                                                            </span>
                                                                        </label>
                                                                    )
                                                                )
                                                            )}
                                                        </div>
                                                        <p className="text-xs text-gray-500 mt-1">
                                                            Select which
                                                            facilities will
                                                            offer this program.
                                                            You can add more
                                                            facilities later.
                                                        </p>
                                                        <FormMessage />
                                                    </FormItem>
                                                )}
                                            />
                                        )}
                                    </div>
                                </div>

                                <div className="flex justify-end gap-3 pt-4 border-t border-gray-200">
                                    <Button
                                        type="button"
                                        variant="outline"
                                        onClick={() => {
                                            setShowAddProgram(false);
                                            programForm.reset(emptyProgramForm);
                                        }}
                                        className="border-gray-300 dark:!bg-[rgba(38,38,38,0.3)]"
                                    >
                                        Cancel
                                    </Button>
                                    <Button
                                        type="submit"
                                        className="bg-brand hover:bg-brand-dark text-white"
                                        disabled={isSubmitting}
                                    >
                                        {isSubmitting
                                            ? 'Creating...'
                                            : 'Create Program'}
                                    </Button>
                                </div>
                            </form>
                        </Form>
                    </div>
                )}

                {programs.length === 0 ? (
                    <div className="card-block p-12 text-center">
                        <p className="text-gray-600 mb-2">No programs found</p>
                        <p className="text-sm text-gray-500">
                            Try adjusting your search or filters
                        </p>
                    </div>
                ) : (
                    <>
                        {isDeptAdminUser ? (
                            <ProgramsTable
                                programs={programs}
                                pollExhausted={canvasPollExhausted}
                                onRowClick={(programId) =>
                                    navigate('/programs/' + programId)
                                }
                            />
                        ) : (
                            <div className="grid grid-cols-2 gap-6 mb-4">
                                {programs.map((program) => (
                                    <ProgramCard
                                        key={program.program_id}
                                        program={program}
                                        showFacilities={false}
                                        pollExhausted={canvasPollExhausted}
                                        onClick={() =>
                                            navigate(
                                                '/programs/' +
                                                    program.program_id
                                            )
                                        }
                                    />
                                ))}
                            </div>
                        )}

                        {/* Pagination */}
                        {(resp?.meta.total ?? 0) > 0 && (
                            <div className="mt-6">
                                <Pagination
                                    currentPage={page}
                                    totalItems={resp?.meta.total ?? 0}
                                    itemsPerPage={perPage}
                                    onPageChange={setPage}
                                    onItemsPerPageChange={setPerPage}
                                    itemLabel="items"
                                />
                            </div>
                        )}
                    </>
                )}
            </div>
        </div>
    );
}

function StatCard({
    label,
    value,
    tooltip
}: {
    label: string;
    value: ReactNode;
    tooltip?: string;
}) {
    const cardContent = (
        <div className="card-block p-6 cursor-help">
            <p className="text-3xl text-brand-dark mb-1">{value}</p>
            <p className="text-sm text-gray-600">{label}</p>
        </div>
    );

    if (!tooltip) {
        return cardContent;
    }

    return (
        <Tooltip>
            <TooltipTrigger asChild>{cardContent}</TooltipTrigger>
            <TooltipContent className="bg-brand-dark text-white max-w-xs">
                {tooltip}
            </TooltipContent>
        </Tooltip>
    );
}

function ProgramCardSkeleton({
    exhausted,
    sourceLabel
}: {
    exhausted: boolean;
    sourceLabel: string;
}) {
    return (
        <Card className="bg-white !p-0">
            <CardContent className="pt-6">
                <div className="flex items-start justify-between gap-3 mb-4">
                    <div className="flex-1 space-y-2">
                        <Skeleton className="h-5 w-3/4" />
                        <Skeleton className="h-4 w-1/3" />
                        <Skeleton className="h-4 w-full" />
                    </div>
                    <Skeleton className="h-6 w-16 rounded-full" />
                </div>
                <div className="flex gap-2 mb-4">
                    <Skeleton className="h-5 w-16 rounded-full" />
                    <Skeleton className="h-5 w-20 rounded-full" />
                </div>
                <div className="grid grid-cols-2 gap-3 mb-4">
                    <Skeleton className="h-16 rounded-md" />
                    <Skeleton className="h-16 rounded-md" />
                </div>
                <div className="flex items-center gap-2 mt-2">
                    {exhausted ? (
                        <span className="text-xs text-amber-600">
                            Taking longer than expected —{' '}
                            <button
                                className="underline"
                                onClick={() => window.location.reload()}
                            >
                                refresh to retry
                            </button>
                        </span>
                    ) : (
                        <>
                            <Loader2 className="h-3 w-3 animate-spin text-blue-500" />
                            <span className="text-xs text-blue-600">
                                Syncing from {sourceLabel}…
                            </span>
                        </>
                    )}
                </div>
            </CardContent>
        </Card>
    );
}

function ProgramCard({
    program,
    showFacilities,
    onClick,
    pollExhausted
}: {
    program: ProgramsOverviewTable;
    showFacilities: boolean;
    onClick: () => void;
    pollExhausted?: boolean;
}) {
    if (isExternalSource(program.source) && program.loading) {
        return (
            <ProgramCardSkeleton
                exhausted={!!pollExhausted}
                sourceLabel={externalSourceLabel(program.source)}
            />
        );
    }
    const status = getEffectiveStatus(program);
    const types = parseCommaSeparated(program.program_types);
    const credits = parseCommaSeparated(program.credit_types);
    const funding = program.funding_type
        ? program.funding_type.replace(/_/g, ' ')
        : '';

    return (
        <Card
            className="group cursor-pointer hover:shadow-lg hover:border-brand transition-all bg-white !p-0"
            onClick={onClick}
        >
            <CardContent className="pt-6">
                <div className="flex items-start justify-between gap-3 mb-4">
                    <div className="flex-1">
                        <h3 className="text-brand-dark mb-1 group-hover:text-brand transition-colors">
                            {program.program_name}
                        </h3>
                        {isExternalSource(program.source) && (
                            <Badge
                                variant="outline"
                                className="text-xs bg-blue-50 text-blue-700 border-blue-200 mb-1 inline-flex"
                            >
                                Synced from{' '}
                                {externalSourceLabel(program.source)}
                            </Badge>
                        )}
                        {program.description && (
                            <p className="text-sm text-gray-600 line-clamp-2">
                                {program.description}
                            </p>
                        )}
                    </div>
                    <Badge
                        variant="outline"
                        className={`${statusColors[status]} ml-3 shrink-0`}
                    >
                        {status}
                    </Badge>
                </div>

                {/* Category Badges */}
                <div className="flex flex-wrap gap-2 mb-4">
                    {types.map((type) => (
                        <Badge
                            key={type}
                            variant="outline"
                            className={`${programTypeColors[type] || 'bg-gray-100 text-gray-700 border-gray-200'} text-xs`}
                        >
                            {formatDisplayName(type)}
                        </Badge>
                    ))}
                </div>

                <div className="grid grid-cols-2 gap-3  mb-4">
                    <MetricBox
                        label="Enrollment"
                        primaryValue={program.total_active_enrollments ?? 0}
                        primaryLabel="currently enrolled"
                        secondaryLines={[
                            {
                                value: `${program.total_enrollments ?? 0} total enrollment${(program.total_enrollments ?? 0) !== 1 ? 's' : ''}`
                            },
                            {
                                value:
                                    program.completion_rate !== null
                                        ? `${Math.round(program.completion_rate)}% completion rate`
                                        : '— completion rate'
                            }
                        ]}
                    />
                    <MetricBox
                        label="Classes"
                        primaryValue={program.total_active_classes ?? 0}
                        primaryLabel="active"
                        secondaryLines={[
                            {
                                value: `${program.total_classes ?? 0} total class${(program.total_classes ?? 0) !== 1 ? 'es' : ''}`
                            }
                        ]}
                    />
                </div>
                {/* Credit & Funding Type */}
                <div className="text-xs text-gray-600 mb-3 space-y-1">
                    {credits.length > 0 && (
                        <div>
                            <span className="text-gray-500">
                                Credit:{' '}
                                {credits.map(formatDisplayName).join(', ')}
                            </span>
                        </div>
                    )}
                    {funding && (
                        <div>
                            <span className="text-gray-500">
                                Funding: {funding}
                            </span>
                        </div>
                    )}
                </div>
                {/* Department Admin: Facility Count */}
                {showFacilities &&
                    (program.total_active_facilities ?? 0) > 0 && (
                        <div className="pt-3 border-t border-gray-200">
                            <div className="text-sm text-gray-600">
                                Active in{' '}
                                <span className="text-brand-dark font-medium">
                                    {program.total_active_facilities}
                                </span>{' '}
                                {program.total_active_facilities === 1
                                    ? 'facility'
                                    : 'facilities'}
                            </div>
                        </div>
                    )}
            </CardContent>
        </Card>
    );
}

function MetricBox({
    label,
    primaryValue,
    primaryLabel,
    secondaryLines
}: {
    label: string;
    primaryValue: number;
    primaryLabel: string;
    secondaryLines?: { value: string | number }[];
}) {
    return (
        <div className="bg-surface-hover rounded-lg p-3">
            <div className="text-xs text-gray-600 mb-2">{label}</div>
            <div className="space-y-1">
                <div className="flex items-baseline gap-1.5">
                    <span className="text-2xl text-brand-dark">
                        {primaryValue}
                    </span>
                    <span className="text-xs text-gray-500">
                        {primaryLabel}
                    </span>
                </div>
                {secondaryLines?.map((line, index) => (
                    <div key={index} className="text-xs text-gray-500">
                        {line.value}
                    </div>
                ))}
            </div>
        </div>
    );
}

function getHistoricalEnrollments(program: ProgramsOverviewTable): number {
    return (
        (program.total_enrollments ?? 0) -
        (program.total_active_enrollments ?? 0)
    );
}

function getUtilizationRate(program: ProgramsOverviewTable): number {
    const capacity = program.total_capacity ?? 0;

    const filledSeats = program.total_filled_seats ?? 0;
    return capacity > 0 ? Math.round((filledSeats / capacity) * 100) : 0;
}

function getPercentageColorClass(percentage: number): string {
    if (percentage >= 75) return 'text-brand';
    if (percentage >= 50) return 'text-brand-gold';
    return 'text-red-600';
}

function ProgramsTable({
    programs,
    onRowClick,
    pollExhausted
}: {
    programs: ProgramsOverviewTable[];
    onRowClick: (programId: number) => void;
    pollExhausted?: boolean;
}) {
    const navigate = useNavigate();
    const nameRefs = useRef<Map<number, HTMLDivElement>>(new Map());
    const [truncatedIds, setTruncatedIds] = useState<Set<number>>(new Set());

    useEffect(() => {
        const recompute = () =>
            setTruncatedIds((prev) => {
                const next = new Set<number>();
                nameRefs.current.forEach((el, id) => {
                    if (el.scrollWidth > el.clientWidth) next.add(id);
                });
                if (
                    next.size === prev.size &&
                    [...next].every((id) => prev.has(id))
                ) {
                    return prev;
                }
                return next;
            });
        recompute();
        const observer = new ResizeObserver(recompute);
        nameRefs.current.forEach((el) => observer.observe(el));
        return () => observer.disconnect();
    }, [programs]);

    return (
        <TooltipProvider>
            <div className="card-block overflow-hidden">
                <Table>
                    <TableHeader className="bg-surface-hover [&_tr]:border-b-0">
                        <TableRow>
                            <TableHead className="text-left px-6 py-4 text-sm font-bold text-brand-dark w-[30%]">
                                Program
                            </TableHead>
                            <TableHead className="text-left px-6 py-4 text-sm font-bold text-brand-dark w-[16%]">
                                Classes
                            </TableHead>
                            <TableHead className="text-left px-6 py-4 text-sm font-bold text-brand-dark w-[16%]">
                                Enrollment
                            </TableHead>
                            <TableHead className="text-left px-6 py-4 text-sm font-bold text-brand-dark w-[16%]">
                                Capacity
                            </TableHead>
                            <TableHead className="text-left px-6 py-4 text-sm font-bold text-brand-dark w-[8%]">
                                Completion
                            </TableHead>
                            <TableHead className="text-left px-6 py-4 text-sm font-bold text-brand-dark w-[8%]">
                                Attendance
                            </TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {programs.map((program) => {
                            if (
                                isExternalSource(program.source) &&
                                program.loading
                            ) {
                                return (
                                    <TableRow key={program.program_id}>
                                        <TableCell className="px-6 py-4">
                                            <div className="space-y-1.5">
                                                <Skeleton className="h-4 w-48" />
                                                {pollExhausted ? (
                                                    <span className="text-xs text-amber-600">
                                                        Taking longer than
                                                        expected —{' '}
                                                        <button
                                                            className="underline"
                                                            onClick={() =>
                                                                window.location.reload()
                                                            }
                                                        >
                                                            refresh to retry
                                                        </button>
                                                    </span>
                                                ) : (
                                                    <div className="flex items-center gap-1.5">
                                                        <Loader2 className="h-3 w-3 animate-spin text-blue-500" />
                                                        <span className="text-xs text-blue-600">
                                                            Syncing from{' '}
                                                            {externalSourceLabel(
                                                                program.source
                                                            )}
                                                            …
                                                        </span>
                                                    </div>
                                                )}
                                            </div>
                                        </TableCell>
                                        {Array.from({ length: 5 }).map(
                                            (_, i) => (
                                                <TableCell
                                                    key={i}
                                                    className="px-6 py-4"
                                                >
                                                    <Skeleton className="h-4 w-12" />
                                                </TableCell>
                                            )
                                        )}
                                    </TableRow>
                                );
                            }
                            const status = getEffectiveStatus(program);
                            const types = parseCommaSeparated(
                                program.program_types
                            );
                            const utilizationRate = getUtilizationRate(program);
                            const historicalEnrollments =
                                getHistoricalEnrollments(program);
                            const completionRate = program.completion_rate;
                            const attendanceRate = program.attendance_rate;

                            return (
                                <TableRow
                                    key={program.program_id}
                                    className={`hover:bg-surface-hover/50 transition-colors cursor-pointer ${
                                        status ===
                                        ProgramEffectiveStatus.Archived
                                            ? 'opacity-40'
                                            : status ===
                                                ProgramEffectiveStatus.Inactive
                                              ? 'opacity-60'
                                              : ''
                                    }`}
                                    {...clickableProps(() =>
                                        onRowClick(program.program_id)
                                    )}
                                >
                                    <TableCell className="px-6 py-4 max-w-0">
                                        <div>
                                            {(status ===
                                                ProgramEffectiveStatus.Inactive ||
                                                status ===
                                                    ProgramEffectiveStatus.Archived) && (
                                                <Badge
                                                    variant="outline"
                                                    className={`${statusColors[status]} text-xs mb-1.5`}
                                                >
                                                    {status}
                                                </Badge>
                                            )}
                                            <Tooltip>
                                                <TooltipTrigger asChild>
                                                    <div
                                                        ref={(el) => {
                                                            if (el) {
                                                                nameRefs.current.set(
                                                                    program.program_id,
                                                                    el
                                                                );
                                                            } else {
                                                                nameRefs.current.delete(
                                                                    program.program_id
                                                                );
                                                            }
                                                        }}
                                                        className="text-base text-brand-dark hover:text-brand transition-colors font-medium mb-1.5 truncate"
                                                    >
                                                        {program.program_name}
                                                    </div>
                                                </TooltipTrigger>
                                                {truncatedIds.has(
                                                    program.program_id
                                                ) && (
                                                    <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                        {program.program_name}
                                                    </TooltipContent>
                                                )}
                                            </Tooltip>
                                            {isExternalSource(
                                                program.source
                                            ) && (
                                                <Badge
                                                    variant="outline"
                                                    className="text-xs bg-blue-50 text-blue-700 border-blue-200 mb-1"
                                                >
                                                    Synced from{' '}
                                                    {externalSourceLabel(
                                                        program.source
                                                    )}
                                                </Badge>
                                            )}
                                        </div>
                                        <div className="flex flex-wrap gap-1.5 items-center">
                                            {types.slice(0, 3).map((type) => (
                                                <Badge
                                                    key={type}
                                                    variant="outline"
                                                    className={`${programTypeColors[type] || 'bg-gray-100 text-gray-700 border-gray-200'} text-xs`}
                                                >
                                                    {formatDisplayName(type)}
                                                </Badge>
                                            ))}
                                            {types.length > 3 && (
                                                <Badge
                                                    variant="outline"
                                                    className="text-xs bg-gray-100 text-gray-600 border-gray-200"
                                                >
                                                    +{types.length - 3}
                                                </Badge>
                                            )}
                                            {(program.total_active_facilities ??
                                                0) > 0 && (
                                                <>
                                                    <span className="text-gray-300">
                                                        •
                                                    </span>
                                                    <Tooltip>
                                                        <TooltipTrigger asChild>
                                                            <button
                                                                className="text-xs text-gray-500 hover:text-gray-500 no-underline"
                                                                onClick={(
                                                                    e
                                                                ) => {
                                                                    e.stopPropagation();
                                                                    navigate(
                                                                        `/facilities?program=${program.program_id}`
                                                                    );
                                                                }}
                                                            >
                                                                {
                                                                    program.total_active_facilities
                                                                }{' '}
                                                                {program.total_active_facilities ===
                                                                1
                                                                    ? 'facility'
                                                                    : 'facilities'}
                                                            </button>
                                                        </TooltipTrigger>
                                                        {!!program
                                                            .facility_names
                                                            ?.length && (
                                                            <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                                <ul className="max-h-64 overflow-y-auto">
                                                                    {program.facility_names.map(
                                                                        (
                                                                            name,
                                                                            index
                                                                        ) => (
                                                                            <li
                                                                                key={
                                                                                    index
                                                                                }
                                                                            >
                                                                                {
                                                                                    name
                                                                                }
                                                                            </li>
                                                                        )
                                                                    )}
                                                                </ul>
                                                            </TooltipContent>
                                                        )}
                                                    </Tooltip>
                                                </>
                                            )}
                                        </div>
                                    </TableCell>
                                    <TableCell className="px-6 py-4">
                                        <div className="text-sm text-gray-700">
                                            <div>
                                                <span className="font-medium text-brand-dark">
                                                    {program.total_active_classes ??
                                                        0}
                                                </span>
                                                <Tooltip>
                                                    <TooltipTrigger asChild>
                                                        <span className="text-gray-500 cursor-help">
                                                            {' '}
                                                            active
                                                        </span>
                                                    </TooltipTrigger>
                                                    <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                        Classes currently
                                                        running with enrolled
                                                        residents
                                                    </TooltipContent>
                                                </Tooltip>
                                            </div>
                                            <Tooltip>
                                                <TooltipTrigger asChild>
                                                    <div className="text-xs text-gray-500 mt-0.5 cursor-help w-fit">
                                                        {program.total_classes ??
                                                            0}{' '}
                                                        total
                                                    </div>
                                                </TooltipTrigger>
                                                <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                    All classes for this program
                                                    (active, completed, and
                                                    scheduled)
                                                </TooltipContent>
                                            </Tooltip>
                                        </div>
                                    </TableCell>
                                    <TableCell className="px-6 py-4">
                                        <div className="text-sm text-gray-700">
                                            <div>
                                                <span className="font-medium text-brand-dark">
                                                    {program.total_active_enrollments ??
                                                        0}
                                                </span>
                                                <Tooltip>
                                                    <TooltipTrigger asChild>
                                                        <span className="text-gray-500 cursor-help">
                                                            {' '}
                                                            currently
                                                        </span>
                                                    </TooltipTrigger>
                                                    <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                        Residents currently
                                                        enrolled in this
                                                        program. A single
                                                        resident can be enrolled
                                                        in more than one class.
                                                    </TooltipContent>
                                                </Tooltip>
                                            </div>
                                            <Tooltip>
                                                <TooltipTrigger asChild>
                                                    <div className="text-xs text-gray-500 mt-0.5 cursor-help w-fit">
                                                        {historicalEnrollments}{' '}
                                                        all-time
                                                    </div>
                                                </TooltipTrigger>
                                                <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                    Past enrollments including
                                                    completed, withdrawn,
                                                    dropped, failed to complete,
                                                    and transferred
                                                </TooltipContent>
                                            </Tooltip>
                                        </div>
                                    </TableCell>
                                    <TableCell className="px-6 py-4">
                                        {isExternalSource(program.source) ? (
                                            <div className="text-sm text-gray-400">
                                                —
                                            </div>
                                        ) : (
                                            <div className="text-sm">
                                                <div className="font-medium text-brand-dark">
                                                    {program.total_capacity ??
                                                        0}
                                                </div>
                                                <Tooltip>
                                                    <TooltipTrigger asChild>
                                                        <div className="text-xs text-gray-500 mt-0.5 cursor-help w-fit">
                                                            {utilizationRate}%
                                                            utilized
                                                        </div>
                                                    </TooltipTrigger>
                                                    <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                        Percentage of available
                                                        capacity currently
                                                        filled
                                                    </TooltipContent>
                                                </Tooltip>
                                            </div>
                                        )}
                                    </TableCell>
                                    <TableCell className="px-6 py-4">
                                        <Tooltip>
                                            <TooltipTrigger asChild>
                                                <div
                                                    className={`text-sm font-medium cursor-help w-fit ${completionRate !== null ? getPercentageColorClass(completionRate) : 'text-gray-500'}`}
                                                >
                                                    {completionRate !== null ? (
                                                        `${Math.round(completionRate)}%`
                                                    ) : (
                                                        <NoDataDash />
                                                    )}
                                                </div>
                                            </TooltipTrigger>
                                            <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                {completionRate !== null
                                                    ? 'Percentage of residents who successfully completed the program out of all who have finished (not including current enrollments)'
                                                    : (program.total_classes ??
                                                            0) === 0
                                                      ? (program.total_cancelled_classes ??
                                                            0) > 0
                                                          ? 'This program has no active or completed classes to calculate a completion rate from'
                                                          : 'No classes have been created for this program yet'
                                                      : 'No residents have reached completion eligibility yet'}
                                            </TooltipContent>
                                        </Tooltip>
                                    </TableCell>
                                    <TableCell className="px-6 py-4">
                                        <Tooltip>
                                            <TooltipTrigger asChild>
                                                <div
                                                    className={`text-sm font-medium cursor-help w-fit ${attendanceRate !== null ? getPercentageColorClass(attendanceRate) : 'text-gray-500'}`}
                                                >
                                                    {attendanceRate !== null ? (
                                                        `${Math.round(attendanceRate)}%`
                                                    ) : (
                                                        <NoDataDash />
                                                    )}
                                                </div>
                                            </TooltipTrigger>
                                            <TooltipContent className="bg-brand-dark text-white max-w-xs">
                                                {attendanceRate !== null
                                                    ? 'Average attendance rate across all active classes in this program'
                                                    : (program.total_classes ??
                                                            0) === 0
                                                      ? (program.total_cancelled_classes ??
                                                            0) > 0
                                                          ? 'This program has no active classes to calculate an attendance rate from'
                                                          : 'No classes have been created for this program yet'
                                                      : 'No classes have started taking attendance yet'}
                                            </TooltipContent>
                                        </Tooltip>
                                    </TableCell>
                                </TableRow>
                            );
                        })}
                    </TableBody>
                </Table>
            </div>
        </TooltipProvider>
    );
}
