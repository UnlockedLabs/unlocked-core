import { useEffect, useMemo, useState } from 'react';
import useSWR from 'swr';
import { toast } from 'sonner';
import { Search } from 'lucide-react';
import {
    ArrowDownTrayIcon,
    ChevronDownIcon,
    ChevronUpIcon,
    InformationCircleIcon
} from '@heroicons/react/24/outline';
import { useAuth, canSwitchFacility } from '@/auth/useAuth';
import { generateReport } from '@/api/reports';
import {
    ReportFormat,
    ReportGenerateRequest,
    ReportType
} from '@/types/reports';
import {
    Cohort,
    Facility,
    ProgramsOverviewTable,
    ProgramType,
    SelectedClassStatus,
    ServerResponseMany,
    User
} from '@/types';
import { Button } from '@/components/ui/button';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import {
    Tooltip,
    TooltipContent,
    TooltipTrigger
} from '@/components/ui/tooltip';
import { MultiSelectFilter } from '@/components/shared';
import { cn } from '@/lib/utils';

type UIFormat = 'CSV' | 'Excel' | 'PDF';
type DateRangeOption = 'all-time' | 'this-month' | 'last-30' | 'custom';

const BTN_ACTIVE =
    'border border-brand bg-brand/10 text-brand-dark dark:text-brand-light dark:bg-brand/20';
const BTN_INACTIVE =
    'bg-white dark:bg-[#171717] border border-gray-200 dark:border-[#262626] text-gray-600 dark:text-gray-400 hover:border-gray-300 dark:hover:border-[#444]';

const FORMAT_TO_API: Record<UIFormat, ReportFormat> = {
    CSV: ReportFormat.CSV,
    Excel: ReportFormat.EXCEL,
    PDF: ReportFormat.PDF
};

// ---- date helpers ----

function toYMD(d: Date): string {
    return d.toISOString().split('T')[0];
}

/** Resolve a preset (or custom range) into concrete YYYY-MM-DD start/end dates. */
function resolveDateRange(
    preset: DateRangeOption,
    from: string,
    to: string
): { start: string; end: string } {
    const now = new Date();
    const today = toYMD(now);
    switch (preset) {
        case 'this-month':
            return {
                start: toYMD(new Date(now.getFullYear(), now.getMonth(), 1)),
                end: toYMD(new Date(now.getFullYear(), now.getMonth() + 1, 0))
            };
        case 'last-30':
            return {
                start: toYMD(
                    new Date(
                        now.getFullYear(),
                        now.getMonth(),
                        now.getDate() - 30
                    )
                ),
                end: today
            };
        case 'custom':
            return { start: from, end: to };
        case 'all-time':
        default:
            // Backend currently requires a bounded range (<=90 days). "All time"
            // relies on a backend follow-up (ticket 650) to relax that for
            // aggregate reports; we send a wide range and surface any error.
            return { start: '2000-01-01', end: today };
    }
}

function buildDates(preset: DateRangeOption, from: string, to: string) {
    const { start, end } = resolveDateRange(preset, from, to);
    return {
        start_date: `${start}T00:00:00Z`,
        end_date: `${end}T23:59:59Z`
    };
}

/** Fire the report request and trigger a browser download of the blob. */
async function runExport(request: ReportGenerateRequest) {
    try {
        const { blob, filename } = await generateReport(request);
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        window.URL.revokeObjectURL(url);
    } catch (err) {
        toast.error(
            err instanceof Error ? err.message : 'Failed to generate export'
        );
    }
}

// ---- shared sub-components ----

function FormatSelector({
    value,
    onChange,
    disabledReasons
}: {
    value: UIFormat;
    onChange: (f: UIFormat) => void;
    /** Map of format -> reason it's disabled (shown as a tooltip). */
    disabledReasons?: Partial<Record<UIFormat, string>>;
}) {
    return (
        <div className="flex gap-2">
            {(['CSV', 'Excel', 'PDF'] as UIFormat[]).map((f) => {
                const reason = disabledReasons?.[f];
                const btn = (
                    <button
                        type="button"
                        aria-disabled={!!reason}
                        onClick={() => {
                            if (!reason) onChange(f);
                        }}
                        className={cn(
                            'rounded-md px-4 py-1.5 text-sm transition-colors',
                            value === f ? BTN_ACTIVE : BTN_INACTIVE,
                            reason && 'cursor-not-allowed opacity-40'
                        )}
                    >
                        {f}
                    </button>
                );
                return reason ? (
                    <Tooltip key={f}>
                        <TooltipTrigger asChild>{btn}</TooltipTrigger>
                        <TooltipContent className="max-w-xs bg-gray-900 text-white">
                            {reason}
                        </TooltipContent>
                    </Tooltip>
                ) : (
                    <span key={f}>{btn}</span>
                );
            })}
        </div>
    );
}

function DateRangeSelector({
    value,
    onChange,
    from,
    to,
    onFromChange,
    onToChange,
    locked = false
}: {
    value: DateRangeOption;
    onChange: (v: DateRangeOption) => void;
    from: string;
    to: string;
    onFromChange: (v: string) => void;
    onToChange: (v: string) => void;
    /** When set, only "Last 30 days" is selectable; the rest are disabled. */
    locked?: boolean;
}) {
    const opts: { value: DateRangeOption; label: string }[] = [
        { value: 'all-time', label: 'All time' },
        { value: 'this-month', label: 'This month' },
        { value: 'last-30', label: 'Last 30 days' },
        { value: 'custom', label: 'Custom range' }
    ];
    return (
        <div className="space-y-2">
            <div className="flex flex-wrap gap-2">
                {opts.map((o) => {
                    const isDisabled = locked && o.value !== 'last-30';
                    return (
                        <button
                            key={o.value}
                            type="button"
                            disabled={isDisabled}
                            onClick={() => !isDisabled && onChange(o.value)}
                            className={cn(
                                'rounded-md px-3 py-1.5 text-sm transition-colors',
                                value === o.value ? BTN_ACTIVE : BTN_INACTIVE,
                                isDisabled && 'cursor-not-allowed opacity-40'
                            )}
                        >
                            {o.label}
                        </button>
                    );
                })}
            </div>
            {value === 'custom' && !locked && (
                <div className="flex items-center gap-2 pt-1">
                    <input
                        type="date"
                        value={from}
                        onChange={(e) => onFromChange(e.target.value)}
                        className="h-9 rounded-md border border-gray-200 bg-white px-3 text-sm text-gray-700 outline-none focus:ring-2 focus:ring-brand dark:border-[#262626] dark:bg-[#171717] dark:text-gray-300"
                    />
                    <span className="text-gray-400">→</span>
                    <input
                        type="date"
                        value={to}
                        onChange={(e) => onToChange(e.target.value)}
                        className="h-9 rounded-md border border-gray-200 bg-white px-3 text-sm text-gray-700 outline-none focus:ring-2 focus:ring-brand dark:border-[#262626] dark:bg-[#171717] dark:text-gray-300"
                    />
                </div>
            )}
        </div>
    );
}

function ColumnTags({ columns }: { columns: string[] }) {
    return (
        <div className="rounded-lg border border-gray-200 p-3 dark:border-[#262626]">
            <p className="mb-2 text-xs text-gray-500 dark:text-gray-400">
                Columns
            </p>
            <div className="flex flex-wrap gap-1.5">
                {columns.map((col) => (
                    <span
                        key={col}
                        className="inline-flex rounded border border-gray-200 bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:border-[#333] dark:bg-[#262626] dark:text-gray-400"
                    >
                        {col}
                    </span>
                ))}
            </div>
        </div>
    );
}

function ExportFooter({
    summary,
    format,
    canExport,
    onExport,
    busy
}: {
    summary: string;
    format: UIFormat;
    canExport: boolean;
    onExport: () => void;
    busy?: boolean;
}) {
    return (
        <div className="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-[#262626]">
            <span className="text-sm text-gray-500 dark:text-gray-400">
                {summary && (
                    <>
                        {summary} ·{' '}
                        <span className="font-medium">{format}</span>
                    </>
                )}
            </span>
            <Button
                onClick={onExport}
                disabled={!canExport || busy}
                className="gap-2 bg-brand text-white hover:bg-brand-dark disabled:cursor-not-allowed disabled:opacity-40"
            >
                <ArrowDownTrayIcon className="size-4" />
                {busy ? 'Exporting…' : 'Export'}
            </Button>
        </div>
    );
}

const residentLabel = (u: User) =>
    `${u.name_last}, ${u.name_first}`.replace(/^, |, $/g, '');

/** Typeahead that searches residents and reports the selected user up. */
function ResidentSearch({
    selected,
    onSelect
}: {
    selected: User | null;
    onSelect: (u: User | null) => void;
}) {
    const [term, setTerm] = useState('');
    const { data } = useSWR<ServerResponseMany<User>>(
        term.length >= 2
            ? `/api/users?search=${encodeURIComponent(term)}&role=student&per_page=8`
            : null
    );
    const results = data?.data ?? [];

    if (selected) {
        return (
            <div className="flex items-center justify-between rounded-md border border-brand bg-white px-3 py-2 dark:bg-[#171717]">
                <div>
                    <span className="text-sm text-gray-900 dark:text-white">
                        {residentLabel(selected)}
                    </span>
                    {selected.doc_id && (
                        <span className="ml-2 text-xs text-gray-500 dark:text-gray-400">
                            {selected.doc_id}
                        </span>
                    )}
                </div>
                <button
                    type="button"
                    onClick={() => {
                        onSelect(null);
                        setTerm('');
                    }}
                    className="text-xs text-gray-500 transition-colors hover:text-red-500"
                >
                    Clear
                </button>
            </div>
        );
    }

    return (
        <div className="relative">
            <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-gray-400" />
            <Input
                placeholder="Search by name or DOC ID..."
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                className="pl-9"
            />
            {results.length > 0 && (
                <div className="absolute left-0 right-0 top-full z-10 mt-1 overflow-hidden rounded-md border border-gray-200 bg-white shadow-lg dark:border-[#262626] dark:bg-[#171717]">
                    {results.map((u) => (
                        <button
                            key={u.id}
                            type="button"
                            onClick={() => {
                                onSelect(u);
                                setTerm('');
                            }}
                            className="flex w-full items-center justify-between px-3 py-2 text-left text-sm transition-colors hover:bg-[#E2E7EA] dark:hover:bg-[#262626]"
                        >
                            <span className="text-gray-900 dark:text-white">
                                {residentLabel(u)}
                            </span>
                            <span className="text-xs text-gray-500 dark:text-gray-400">
                                {u.doc_id}
                            </span>
                        </button>
                    ))}
                </div>
            )}
        </div>
    );
}

// ---- export forms ----

const PROGRAM_TYPE_OPTIONS = Object.values(ProgramType).map((t) => ({
    value: t,
    label: t.replace(/_/g, ' ').replace(/-/g, ' ')
}));

function ProgramsExportForm({ isDeptAdmin }: { isDeptAdmin: boolean }) {
    const { data: facilitiesResp } = useSWR<ServerResponseMany<Facility>>(
        isDeptAdmin ? '/api/facilities' : null
    );
    const { data: programsResp } = useSWR<
        ServerResponseMany<ProgramsOverviewTable>
    >('/api/programs/detailed-list?per_page=100');

    const facilities = facilitiesResp?.data ?? [];
    const programs = programsResp?.data ?? [];

    const [selectedFacilities, setSelectedFacilities] = useState<string[]>([]);
    const [selectedPrograms, setSelectedPrograms] = useState<string[]>([]);
    const [selectedTypes, setSelectedTypes] = useState<string[]>([]);
    const [dateRange, setDateRange] = useState<DateRangeOption>('all-time');
    const [from, setFrom] = useState('');
    const [to, setTo] = useState('');
    const [format, setFormat] = useState<UIFormat>('CSV');
    const [includeClassBreakdown, setIncludeClassBreakdown] = useState(false);
    const [includeInactive, setIncludeInactive] = useState(false);
    const [busy, setBusy] = useState(false);

    const oneFacility = selectedFacilities.length === 1;
    const oneProgram = selectedPrograms.length === 1;
    const canBreakdown = oneFacility && oneProgram;
    const breakdownActive = canBreakdown && includeClassBreakdown;

    // The class-level breakdown only renders in Excel/PDF, so CSV can't stay selected.
    useEffect(() => {
        if (breakdownActive && format === 'CSV') setFormat('Excel');
    }, [breakdownActive, format]);

    const programCount =
        selectedPrograms.length === 0
            ? programs.length
            : selectedPrograms.length;

    const columns = [
        'Program Name',
        'Program Type',
        'Facilities Active',
        'Total Classes',
        'Currently Enrolled',
        'Enrolled in Range',
        'Total Capacity',
        'Utilization %',
        // Appended (not prepended) to match the export's column order.
        ...(includeInactive ? ['Status'] : [])
    ];

    async function handleExport() {
        // Empty OR full selection both mean "all" (no narrowing).
        const facilitiesAll =
            selectedFacilities.length === 0 ||
            selectedFacilities.length === facilities.length;
        const programsAll =
            selectedPrograms.length === 0 ||
            selectedPrograms.length === programs.length;
        const isTypeSubset =
            selectedTypes.length > 0 &&
            selectedTypes.length < PROGRAM_TYPE_OPTIONS.length;
        const request: ReportGenerateRequest = {
            type: ReportType.PROGRAM_OUTCOMES,
            format: FORMAT_TO_API[format],
            ...buildDates(dateRange, from, to),
            // Program-level stats always aggregate active classes; the
            // "Include inactive programs" toggle affects program inclusion only.
            class_status: 'Active',
            // Multi-facility scope; omitted when "all" facilities are in scope.
            facility_ids: facilitiesAll
                ? undefined
                : selectedFacilities.map(Number),
            // Multi-program scope; omitted when "all" programs are in scope.
            program_ids: programsAll ? undefined : selectedPrograms.map(Number),
            program_types: isTypeSubset
                ? (selectedTypes as ProgramType[])
                : undefined,
            include_class_breakdown:
                canBreakdown && includeClassBreakdown ? true : undefined,
            include_inactive: includeInactive || undefined
        };
        setBusy(true);
        await runExport(request);
        setBusy(false);
    }

    return (
        <div className="space-y-5">
            <div className="grid grid-cols-2 gap-4">
                {isDeptAdmin && (
                    <div>
                        <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                            Facilities
                        </label>
                        <MultiSelectFilter
                            label="facilities"
                            allLabel={`All facilities (${facilities.length})`}
                            options={facilities.map((f) => ({
                                value: String(f.id),
                                label: f.name
                            }))}
                            selected={selectedFacilities}
                            onChange={setSelectedFacilities}
                        />
                    </div>
                )}
                <div>
                    <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                        Program
                    </label>
                    <MultiSelectFilter
                        label="programs"
                        allLabel="All programs"
                        options={programs.map((p) => ({
                            value: String(p.program_id),
                            label: p.program_name
                        }))}
                        selected={selectedPrograms}
                        onChange={setSelectedPrograms}
                    />
                </div>
                <div>
                    <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                        Program Type
                    </label>
                    <MultiSelectFilter
                        label="types"
                        allLabel="All program types"
                        options={PROGRAM_TYPE_OPTIONS}
                        selected={selectedTypes}
                        onChange={setSelectedTypes}
                    />
                </div>
            </div>

            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Date range
                </label>
                <DateRangeSelector
                    value={dateRange}
                    onChange={setDateRange}
                    from={from}
                    to={to}
                    onFromChange={setFrom}
                    onToChange={setTo}
                />
            </div>

            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Format
                </label>
                <FormatSelector
                    value={format}
                    onChange={setFormat}
                    disabledReasons={
                        breakdownActive
                            ? {
                                  CSV: "CSV can't include the class-level breakdown — it's a flat, single-table format. Choose Excel or PDF to keep the per-class rows."
                              }
                            : undefined
                    }
                />
            </div>

            <div className="space-y-3">
                <div className="flex items-start gap-2.5">
                    <Checkbox
                        id="class-breakdown"
                        checked={includeClassBreakdown}
                        onCheckedChange={(v) => setIncludeClassBreakdown(!!v)}
                        disabled={!canBreakdown}
                        className="mt-0.5"
                    />
                    <div>
                        <label
                            htmlFor="class-breakdown"
                            className={cn(
                                'cursor-pointer text-sm',
                                canBreakdown
                                    ? 'text-gray-700 dark:text-gray-300'
                                    : 'text-gray-400 dark:text-gray-600'
                            )}
                        >
                            Include class-level breakdown
                        </label>
                        <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                            Available when one program and one facility are
                            selected. Appends class-level rows beneath the
                            program summary.
                        </p>
                    </div>
                </div>
                <div className="flex items-center gap-2.5">
                    <Checkbox
                        id="include-inactive"
                        checked={includeInactive}
                        onCheckedChange={(v) => setIncludeInactive(!!v)}
                    />
                    <label
                        htmlFor="include-inactive"
                        className="cursor-pointer text-sm text-gray-700 dark:text-gray-300"
                    >
                        Include inactive programs
                    </label>
                </div>
            </div>

            <ColumnTags columns={columns} />

            <ExportFooter
                summary={`Approximately ${programCount} program${programCount !== 1 ? 's' : ''}`}
                format={format}
                canExport
                busy={busy}
                onExport={() => void handleExport()}
            />
        </div>
    );
}

// Must cover every key in the backend's rosterStatusFilterMap; otherwise
// "Select all" sends a narrower filter than leaving the list empty, and the
// omitted statuses can never be isolated or included.
const ENROLLMENT_STATUS_OPTIONS = [
    { value: 'Enrolled', label: 'Enrolled' },
    { value: 'Completed', label: 'Completed' },
    { value: 'Cancelled', label: 'Cancelled' },
    { value: 'Withdrawn', label: 'Withdrawn' },
    { value: 'Dropped', label: 'Dropped' },
    { value: 'Segregated', label: 'Segregated' },
    { value: 'Failed to Complete', label: 'Failed to Complete' },
    { value: 'Transferred', label: 'Transferred' }
];
// Statuses stored with an "Incomplete:" prefix (they carry a reason). Cancelled
// is a terminal status without a reason, so it is intentionally excluded here.
const NON_COMPLETING = new Set([
    'Withdrawn',
    'Dropped',
    'Segregated',
    'Failed to Complete',
    'Transferred'
]);

function useClasses() {
    // facility=all lets switch-capable admins (system/dept) see every facility's
    // classes; facility_admins are transparently scoped to their own facility.
    // all=true returns the full list (no pagination cap) for the dropdown.
    const { data } = useSWR<ServerResponseMany<Cohort>>(
        '/api/program-classes?all=true&facility=all'
    );
    return data?.data ?? [];
}

function ClassRosterForm() {
    // Rosters are meaningful for running and finished classes. Facility scope is
    // handled by useClasses (dept/system admins see all facilities; facility
    // admins only their own).
    const classes = useClasses().filter(
        (c) =>
            c.status === SelectedClassStatus.Active ||
            c.status === SelectedClassStatus.Completed
    );
    const [selectedClass, setSelectedClass] = useState('');
    const [selectedStatuses, setSelectedStatuses] = useState<string[]>([
        'Enrolled'
    ]);
    const [format, setFormat] = useState<UIFormat>('CSV');
    const [includeAttendance, setIncludeAttendance] = useState(false);
    const [includeDates, setIncludeDates] = useState(false);
    const [busy, setBusy] = useState(false);

    const selectedClassData = classes.find(
        (c) => String(c.id) === selectedClass
    );
    const canSelect = !!selectedClass;
    const showIncompleteReason = selectedStatuses.some((s) =>
        NON_COMPLETING.has(s)
    );

    const columns = [
        'Resident Name',
        'DOC ID',
        'Enrollment Status',
        ...(showIncompleteReason ? ['Incomplete Reason'] : []),
        ...(includeAttendance ? ['Avg Attendance Rate'] : []),
        ...(includeDates ? ['Enrolled At', 'Ended At'] : [])
    ];

    async function handleExport() {
        const request: ReportGenerateRequest = {
            type: ReportType.CLASS_ROSTER,
            format: FORMAT_TO_API[format],
            // Roster has no date UI; send an all-time range so the backend
            // always receives start/end dates.
            ...buildDates('all-time', '', ''),
            cohort_id: Number(selectedClass),
            enrollment_statuses:
                selectedStatuses.length > 0 ? selectedStatuses : undefined,
            include_incomplete_reason: showIncompleteReason || undefined,
            include_attendance_rate: includeAttendance || undefined,
            include_enrollment_dates: includeDates || undefined
        };
        setBusy(true);
        await runExport(request);
        setBusy(false);
    }

    return (
        <div className="space-y-5">
            <div className="grid grid-cols-2 gap-4">
                <div>
                    <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                        Class <span className="text-red-500">*</span>
                    </label>
                    <Select
                        value={selectedClass}
                        onValueChange={setSelectedClass}
                    >
                        <SelectTrigger>
                            <SelectValue placeholder="Select class..." />
                        </SelectTrigger>
                        <SelectContent>
                            {classes.map((c) => (
                                <SelectItem key={c.id} value={String(c.id)}>
                                    {c.class_name}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
            </div>

            <div>
                <label className="mb-1.5 flex items-center gap-1 text-sm text-gray-700 dark:text-gray-300">
                    Enrollment Status
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <InformationCircleIcon className="size-4 cursor-help text-gray-400" />
                        </TooltipTrigger>
                        <TooltipContent className="max-w-xs bg-gray-900 text-white">
                            Select which enrollment statuses to include. An
                            Incomplete Reason column is added automatically when
                            a non-completing status is selected.
                        </TooltipContent>
                    </Tooltip>
                </label>
                <div className="max-w-xs">
                    <MultiSelectFilter
                        label="enrollment-status"
                        allLabel="All statuses"
                        options={ENROLLMENT_STATUS_OPTIONS}
                        selected={selectedStatuses}
                        onChange={setSelectedStatuses}
                    />
                </div>
            </div>

            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Format
                </label>
                <FormatSelector value={format} onChange={setFormat} />
            </div>

            <div className="space-y-3">
                <div className="flex items-center gap-2.5">
                    <Checkbox
                        id="incl-attendance"
                        checked={includeAttendance}
                        onCheckedChange={(v) => setIncludeAttendance(!!v)}
                    />
                    <label
                        htmlFor="incl-attendance"
                        className="cursor-pointer text-sm text-gray-700 dark:text-gray-300"
                    >
                        Include average attendance rate
                    </label>
                </div>
                <div className="flex items-center gap-2.5">
                    <Checkbox
                        id="incl-dates"
                        checked={includeDates}
                        onCheckedChange={(v) => setIncludeDates(!!v)}
                    />
                    <label
                        htmlFor="incl-dates"
                        className="cursor-pointer text-sm text-gray-700 dark:text-gray-300"
                    >
                        Include enrolled at / ended at dates
                    </label>
                </div>
            </div>

            <ColumnTags columns={columns} />

            <ExportFooter
                summary={
                    canSelect
                        ? `${selectedClassData?.enrolled ?? 0} residents`
                        : ''
                }
                format={format}
                canExport={canSelect}
                busy={busy}
                onExport={() => void handleExport()}
            />
            {!canSelect && (
                <p className="-mt-2 text-sm text-gray-500 dark:text-gray-400">
                    Select a class to continue.
                </p>
            )}
        </div>
    );
}

function AttendanceRecordsForm() {
    const classes = useClasses();
    const [selectedClass, setSelectedClass] = useState('all');
    const [resident, setResident] = useState<User | null>(null);
    const [dateRange, setDateRange] = useState<DateRangeOption>('last-30');
    const [from, setFrom] = useState('');
    const [to, setTo] = useState('');
    const [format, setFormat] = useState<UIFormat>('CSV');
    const [busy, setBusy] = useState(false);

    const noFilters = selectedClass === 'all' && !resident;

    // With no class/resident selected, the export spans every class, so lock the
    // range to Last 30 days (the selector disables the other options too).
    useEffect(() => {
        if (noFilters) setDateRange('last-30');
    }, [noFilters]);

    async function handleExport() {
        const effectiveRange: DateRangeOption = noFilters
            ? 'last-30'
            : dateRange;
        const request: ReportGenerateRequest = {
            type: ReportType.ATTENDANCE,
            format: FORMAT_TO_API[format],
            ...buildDates(effectiveRange, from, to),
            cohort_id:
                selectedClass !== 'all' ? Number(selectedClass) : undefined,
            user_id: resident ? resident.id : undefined
        };
        setBusy(true);
        await runExport(request);
        setBusy(false);
    }

    return (
        <div className="space-y-5">
            <div className="grid grid-cols-2 gap-4">
                <div>
                    <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                        Class
                    </label>
                    <Select
                        value={selectedClass}
                        onValueChange={setSelectedClass}
                    >
                        <SelectTrigger>
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All classes</SelectItem>
                            {classes.map((c) => (
                                <SelectItem key={c.id} value={String(c.id)}>
                                    {c.class_name}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                </div>
                <div>
                    <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                        Resident
                    </label>
                    <ResidentSearch
                        selected={resident}
                        onSelect={setResident}
                    />
                </div>
            </div>

            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Date
                </label>
                <DateRangeSelector
                    value={dateRange}
                    onChange={setDateRange}
                    from={from}
                    to={to}
                    onFromChange={setFrom}
                    onToChange={setTo}
                    locked={noFilters}
                />
            </div>

            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Format
                </label>
                <FormatSelector value={format} onChange={setFormat} />
            </div>

            {noFilters && (
                <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-900/10">
                    <InformationCircleIcon className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
                    <p className="text-xs text-amber-700 dark:text-amber-400">
                        No class or resident selected — this pulls every record
                        across all classes, so the date range is locked to{' '}
                        <strong>Last 30 days</strong> to keep the export
                        manageable.
                    </p>
                </div>
            )}

            <ColumnTags
                columns={[
                    'Date',
                    'Program Name',
                    'Class Name',
                    'Facility',
                    'Resident Name',
                    'DOC ID',
                    'Attendance Status',
                    'Note'
                ]}
            />

            <ExportFooter
                summary="Attendance records"
                format={format}
                canExport
                busy={busy}
                onExport={() => void handleExport()}
            />
        </div>
    );
}

function ResidentProfileForm() {
    const [resident, setResident] = useState<User | null>(null);
    const [format, setFormat] = useState<UIFormat>('CSV');
    const [busy, setBusy] = useState(false);

    async function handleExport() {
        if (!resident) return;
        const request: ReportGenerateRequest = {
            type: ReportType.RESIDENT_PROFILE,
            format: FORMAT_TO_API[format],
            // No date UI; send an all-time range so start/end are always present.
            ...buildDates('all-time', '', ''),
            user_id: resident.id
        };
        setBusy(true);
        await runExport(request);
        setBusy(false);
    }

    return (
        <div className="space-y-5">
            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Resident <span className="text-red-500">*</span>
                </label>
                <ResidentSearch selected={resident} onSelect={setResident} />
            </div>

            <div>
                <label className="mb-1.5 block text-sm text-gray-700 dark:text-gray-300">
                    Format
                </label>
                <FormatSelector value={format} onChange={setFormat} />
            </div>

            <ColumnTags
                columns={[
                    'Resident Name',
                    'DOC ID',
                    'Facility',
                    'Program Name',
                    'Class Name',
                    'Enrollment Status',
                    'Enrolled Date',
                    'End Date',
                    'Sessions Attended',
                    'Total Sessions',
                    'Attendance Rate',
                    'Completion Status'
                ]}
            />
            <p className="-mt-2 text-xs text-gray-500 dark:text-gray-400">
                One row per enrollment (active or completed)
            </p>

            <ExportFooter
                summary={resident ? '1 resident' : ''}
                format={format}
                canExport={!!resident}
                busy={busy}
                onExport={() => void handleExport()}
            />
            {!resident && (
                <p className="-mt-2 text-sm text-gray-500 dark:text-gray-400">
                    Select a resident to continue.
                </p>
            )}
        </div>
    );
}

// ---- export registry ----

type ExportId =
    | 'programs-export'
    | 'class-roster'
    | 'attendance-records'
    | 'resident-profile';

interface ExportItem {
    id: ExportId;
    name: string;
    description: string;
}

const EXPORT_GROUPS: {
    label: string;
    description: string;
    items: ExportItem[];
}[] = [
    {
        label: 'Programs',
        description:
            'Program performance and enrollment — across or within facilities.',
        items: [
            {
                id: 'programs-export',
                name: 'Programs Export',
                description:
                    "Flexible program report — filter by facility and program to get system-wide totals, per-facility breakdowns, or a single program's class-level detail."
            }
        ]
    },
    {
        label: 'Classes',
        description: 'Rosters and attendance records for a class.',
        items: [
            {
                id: 'class-roster',
                name: 'Class Roster',
                description:
                    'List of residents enrolled in a class — optionally with attendance stats and enrollment dates.'
            },
            {
                id: 'attendance-records',
                name: 'Attendance Records',
                description:
                    'One row per resident per session. Filter by class, resident, or date to narrow results.'
            }
        ]
    },
    {
        label: 'Residents',
        description: 'Individual resident profiles and attendance history.',
        items: [
            {
                id: 'resident-profile',
                name: 'Resident Profile Export',
                description:
                    "Full snapshot of a resident's program participation history."
            }
        ]
    }
];

const FORMAT_BADGES: UIFormat[] = ['CSV', 'Excel', 'PDF'];

function ExportFormBody({
    id,
    isDeptAdmin
}: {
    id: ExportId;
    isDeptAdmin: boolean;
}) {
    switch (id) {
        case 'programs-export':
            return <ProgramsExportForm isDeptAdmin={isDeptAdmin} />;
        case 'class-roster':
            return <ClassRosterForm />;
        case 'attendance-records':
            return <AttendanceRecordsForm />;
        case 'resident-profile':
            return <ResidentProfileForm />;
    }
}

export default function Exports() {
    const { user } = useAuth();
    const isDeptAdmin = user ? canSwitchFacility(user) : false;
    const [selectedId, setSelectedId] = useState<ExportId>('programs-export');
    const [search, setSearch] = useState('');

    const allItems = useMemo(
        () =>
            EXPORT_GROUPS.flatMap((g) =>
                g.items.map((i) => ({ ...i, groupLabel: g.label }))
            ),
        []
    );
    const filteredItems = search
        ? allItems.filter((i) =>
              i.name.toLowerCase().includes(search.toLowerCase())
          )
        : null;

    const activeGroup = EXPORT_GROUPS.find((g) =>
        g.items.some((i) => i.id === selectedId)
    );

    return (
        <div className="mx-auto max-w-7xl px-6 py-8">
            <div className="mb-8">
                <h1 className="page-title">Exports &amp; Reports</h1>
                <p className="page-subtitle">
                    Run any export from one place — pick what you need, set the
                    range and format, and download.
                </p>
            </div>

            <div className="flex items-start gap-6">
                {/* Left nav */}
                <div className="w-60 shrink-0 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-[#262626] dark:bg-[#171717]">
                    <div className="border-b border-gray-100 p-3 dark:border-[#262626]">
                        <div className="relative">
                            <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-gray-400" />
                            <Input
                                placeholder="Search exports..."
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                className="h-8 pl-8 text-sm"
                            />
                        </div>
                    </div>
                    <div className="p-2">
                        {filteredItems ? (
                            filteredItems.length === 0 ? (
                                <p className="px-2 py-3 text-xs text-gray-400">
                                    No exports found
                                </p>
                            ) : (
                                filteredItems.map((item) => (
                                    <NavButton
                                        key={item.id}
                                        active={selectedId === item.id}
                                        onClick={() => {
                                            setSelectedId(item.id);
                                            setSearch('');
                                        }}
                                        name={item.name}
                                    />
                                ))
                            )
                        ) : (
                            EXPORT_GROUPS.map((group) => (
                                <div key={group.label} className="mb-1">
                                    <p className="px-3 py-2 text-xs uppercase tracking-wider text-gray-400 dark:text-gray-500">
                                        {group.label}
                                    </p>
                                    {group.items.map((item) => (
                                        <NavButton
                                            key={item.id}
                                            active={selectedId === item.id}
                                            onClick={() =>
                                                setSelectedId(item.id)
                                            }
                                            name={item.name}
                                        />
                                    ))}
                                </div>
                            ))
                        )}
                    </div>
                </div>

                {/* Right content */}
                <div className="min-w-0 flex-1">
                    {activeGroup && (
                        <div className="mb-6">
                            <h2 className="text-brand-dark dark:text-white">
                                {activeGroup.label}
                            </h2>
                            <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
                                {activeGroup.description}
                            </p>
                        </div>
                    )}

                    <div className="space-y-4">
                        {activeGroup?.items.map((item) => {
                            const isOpen = item.id === selectedId;
                            return (
                                <div
                                    key={item.id}
                                    className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-[#262626] dark:bg-[#171717]"
                                >
                                    <button
                                        type="button"
                                        className="flex w-full items-start justify-between px-6 py-4 text-left transition-colors hover:bg-gray-50 dark:hover:bg-[#262626]/50"
                                        onClick={() => setSelectedId(item.id)}
                                    >
                                        <div className="flex-1 pr-4">
                                            <h3 className="text-brand-dark dark:text-white">
                                                {item.name}
                                            </h3>
                                            <p className="mt-0.5 text-sm text-gray-600 dark:text-gray-400">
                                                {item.description}
                                            </p>
                                        </div>
                                        <div className="flex shrink-0 items-center gap-3">
                                            <div className="flex gap-1">
                                                {FORMAT_BADGES.map((f) => (
                                                    <span
                                                        key={f}
                                                        className="rounded border border-gray-200 px-1.5 py-0.5 text-xs text-gray-500 dark:border-[#262626] dark:text-gray-400"
                                                    >
                                                        {f}
                                                    </span>
                                                ))}
                                            </div>
                                            {isOpen ? (
                                                <ChevronUpIcon className="size-4 text-gray-400" />
                                            ) : (
                                                <ChevronDownIcon className="size-4 text-gray-400" />
                                            )}
                                        </div>
                                    </button>

                                    {isOpen && (
                                        <div className="border-t border-gray-100 px-6 pb-6 pt-5 dark:border-[#262626]">
                                            <ExportFormBody
                                                id={item.id}
                                                isDeptAdmin={isDeptAdmin}
                                            />
                                        </div>
                                    )}
                                </div>
                            );
                        })}
                    </div>
                </div>
            </div>
        </div>
    );
}

function NavButton({
    active,
    onClick,
    name
}: {
    active: boolean;
    onClick: () => void;
    name: string;
}) {
    return (
        <button
            type="button"
            onClick={onClick}
            className={cn(
                'w-full rounded-md px-3 py-2 text-left text-sm transition-colors',
                active
                    ? 'border border-brand bg-brand/5 text-brand-dark dark:text-white'
                    : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-[#262626]'
            )}
        >
            {name}
        </button>
    );
}
