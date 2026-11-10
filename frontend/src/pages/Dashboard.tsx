import { useState, useMemo, ComponentProps } from 'react';
import { useNavigate } from 'react-router-dom';
import useSWR from 'swr';
import { useAuth, canSwitchFacility, hasFeature } from '@/auth/useAuth';
import {
    ClassMetrics,
    FeatureAccess,
    FacilityHealthSummary,
    MissingAttendanceItem,
    TodaysScheduleItem,
    ServerResponseMany,
    ServerResponseOne
} from '@/types';
import { formatTime12h } from '@/lib/formatters';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import {
    Tooltip,
    TooltipTrigger,
    TooltipContent
} from '@/components/ui/tooltip';
import {
    AcademicCapIcon,
    UsersIcon,
    ChartBarIcon,
    RectangleStackIcon,
    ListBulletIcon
} from '@heroicons/react/24/outline';
import { InfoTooltip, PageHeader } from '@/components/shared';
import {
    AlertCircle,
    Calendar,
    CheckCircle,
    ChevronDown,
    ChevronUp,
    Clock,
    ExternalLink
} from 'lucide-react';

function SurfaceCard({ className, ...props }: ComponentProps<'div'>) {
    return (
        <div
            className={cn(
                'bg-white dark:bg-[#171717] rounded-lg border border-gray-200 dark:border-[#262626]',
                className
            )}
            {...props}
        />
    );
}

export default function Dashboard() {
    const { user } = useAuth();
    const deptAdmin = user ? canSwitchFacility(user) : false;
    const programsEnabled =
        !!user && hasFeature(user, FeatureAccess.ProgramAccess);
    const { data: classMetricsResp } = useSWR<ServerResponseOne<ClassMetrics>>(
        !programsEnabled
            ? null
            : deptAdmin
              ? '/api/dashboard/class-metrics?facility=all'
              : '/api/dashboard/class-metrics'
    );
    const { data: missingAttendanceResp, isLoading: missingAttendanceLoading } =
        useSWR<ServerResponseMany<MissingAttendanceItem>>(
            deptAdmin
                ? '/api/program-classes/missing-attendance?facility=all&days=3&all=true'
                : '/api/program-classes/missing-attendance?days=3&all=true'
        );
    const { data: facilityHealthResp } = useSWR<
        ServerResponseOne<FacilityHealthSummary[]>
    >(
        !programsEnabled
            ? null
            : deptAdmin
              ? '/api/dashboard/facility-health?facility=all&days=3'
              : '/api/dashboard/facility-health?days=3'
    );
    const { data: todaysScheduleResp, isLoading: todaysScheduleLoading } =
        useSWR<ServerResponseMany<TodaysScheduleItem>>(
            deptAdmin
                ? '/api/program-classes/todays-schedule?facility=all&all=true'
                : '/api/program-classes/todays-schedule?all=true'
        );
    const todaysScheduleItems = useMemo<TodaysScheduleItem[]>(
        () => todaysScheduleResp?.data ?? ([] as TodaysScheduleItem[]),
        [todaysScheduleResp?.data]
    );

    return (
        <div className="bg-[#E7EAED] dark:bg-[#0a0a0a] min-h-screen overflow-x-hidden">
            <div className="max-w-7xl mx-auto px-6 py-8">
                {deptAdmin ? (
                    <DeptAdminView
                        programsEnabled={programsEnabled}
                        metrics={classMetricsResp?.data}
                        missingAttendance={missingAttendanceResp?.data ?? []}
                        missingAttendanceLoading={missingAttendanceLoading}
                        facilityHealthSummaries={facilityHealthResp?.data ?? []}
                    />
                ) : (
                    <FacilityAdminView
                        programsEnabled={programsEnabled}
                        facilityName={user?.facility.name ?? 'My Facility'}
                        metrics={classMetricsResp?.data}
                        missingAttendance={missingAttendanceResp?.data ?? []}
                        missingAttendanceLoading={missingAttendanceLoading}
                        todaysScheduleItems={todaysScheduleItems}
                        todaysScheduleLoading={todaysScheduleLoading}
                    />
                )}
            </div>
        </div>
    );
}

function FacilityAdminView({
    programsEnabled,
    facilityName,
    metrics,
    missingAttendance,
    missingAttendanceLoading,
    todaysScheduleItems,
    todaysScheduleLoading
}: {
    programsEnabled: boolean;
    facilityName: string;
    metrics?: ClassMetrics;
    missingAttendance: MissingAttendanceItem[];
    missingAttendanceLoading: boolean;
    todaysScheduleItems: TodaysScheduleItem[];
    todaysScheduleLoading: boolean;
}) {
    const navigate = useNavigate();
    const stats = useMemo(() => {
        return buildStats(metrics);
    }, [metrics]);
    const handleAttendanceNavigate = (item: TodaysScheduleItem) => {
        if (!item.event_id) {
            navigate('/program-classes/' + item.cohort_id + '/attendance');
            return;
        }
        navigate(
            `/program-classes/${item.cohort_id}/events/${item.event_id}/attendance/${item.date}`
        );
    };

    return (
        <div>
            <PageHeader
                title="Facility Dashboard"
                subtitle={facilityName}
                className="mb-[15px]"
            />

            {programsEnabled && (
                <MetricCards stats={stats} onNavigate={navigate} />
            )}

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                <div className="lg:col-span-2">
                    <TodaysSchedule
                        items={todaysScheduleItems}
                        isLoading={todaysScheduleLoading}
                        onNavigate={handleAttendanceNavigate}
                    />
                </div>
                <div className="space-y-6">
                    <MissingAttendanceWidget
                        items={missingAttendance}
                        isLoading={missingAttendanceLoading}
                        variant="facility"
                    />
                    <QuickActions navigate={navigate} />
                </div>
            </div>
        </div>
    );
}

function DeptAdminView({
    programsEnabled,
    metrics,
    missingAttendance,
    missingAttendanceLoading,
    facilityHealthSummaries
}: {
    programsEnabled: boolean;
    metrics?: ClassMetrics;
    missingAttendance: MissingAttendanceItem[];
    missingAttendanceLoading: boolean;
    facilityHealthSummaries: FacilityHealthSummary[];
}) {
    const navigate = useNavigate();
    const stats = useMemo(() => {
        return buildStats(metrics);
    }, [metrics]);

    const facilityRows = useMemo(
        () => facilityHealthSummaries,
        [facilityHealthSummaries]
    );

    return (
        <div>
            <PageHeader title="Department Overview" className="mb-[15px]" />

            {programsEnabled && (
                <MetricCards stats={stats} onNavigate={navigate} />
            )}

            <div className="space-y-6">
                {programsEnabled && (
                    <div>
                        <h2 className="text-brand-dark dark:text-white mb-4">
                            Facility Health Overview
                        </h2>
                        <FacilityHealthTable rows={facilityRows} />
                    </div>
                )}
                <div className="flex gap-6 flex-wrap lg:flex-nowrap">
                    <div className="flex-1 lg:flex-[2] min-w-75">
                        <MissingAttendanceWidget
                            items={missingAttendance}
                            isLoading={missingAttendanceLoading}
                            variant="department"
                        />
                    </div>
                    <div className="flex-1 lg:flex-[1] min-w-[250px]">
                        <QuickActions navigate={navigate} />
                    </div>
                </div>
            </div>
        </div>
    );
}

interface MetricCardProps {
    icon: React.ReactNode;
    iconBg: string;
    label: string;
    value: string | number;
    tooltip: string;
    subtitle: string;
    tooltipAction?: React.ReactNode;
}

function MetricCard({
    icon,
    iconBg,
    label,
    value,
    tooltip,
    subtitle,
    tooltipAction
}: MetricCardProps) {
    return (
        <SurfaceCard className="p-4">
            <div className="flex items-center gap-3 mb-3">
                <div className={cn('p-2 rounded', iconBg)}>{icon}</div>
                <div className="text-2xl text-brand-dark dark:text-white">
                    {value}
                </div>
                <InfoTooltip className="ml-auto">
                    <p className={tooltipAction ? 'mb-2' : undefined}>
                        {tooltip}
                    </p>
                    {tooltipAction}
                </InfoTooltip>
            </div>
            <div className="text-sm text-gray-600 dark:text-gray-400">
                {label}
            </div>
            <div className="text-xs text-gray-500 dark:text-gray-500 mt-1">
                {subtitle}
            </div>
        </SurfaceCard>
    );
}

interface DashboardStats {
    activeClasses: number;
    totalEnrollment: number;
    totalCapacity: number;
    capacityUtilization: number;
    attendanceConcerns: number;
    scheduledClasses: number;
}

function buildStats(metrics?: ClassMetrics): DashboardStats {
    if (!metrics) {
        return {
            activeClasses: 0,
            totalEnrollment: 0,
            totalCapacity: 0,
            capacityUtilization: 0,
            attendanceConcerns: 0,
            scheduledClasses: 0
        };
    }

    const totalCapacity = metrics.total_seats;
    const totalEnrollment = metrics.total_enrollments;
    const capacityUtilization =
        totalCapacity > 0
            ? Math.round((totalEnrollment / totalCapacity) * 100)
            : 0;

    return {
        activeClasses: metrics.active_classes,
        totalEnrollment,
        totalCapacity,
        capacityUtilization,
        attendanceConcerns: metrics.attendance_concerns,
        scheduledClasses: metrics.scheduled_classes ?? 0
    };
}

function TodaysSchedule({
    items,
    isLoading,
    onNavigate
}: {
    items: TodaysScheduleItem[];
    isLoading: boolean;
    onNavigate: (item: TodaysScheduleItem) => void;
}) {
    const navigate = useNavigate();
    const today = new Date();
    const dateLabel = today.toLocaleDateString('en-US', {
        weekday: 'long',
        month: 'long',
        day: 'numeric',
        year: 'numeric'
    });

    const todayClasses: TodaysScheduleItem[] =
        useMemo((): TodaysScheduleItem[] => {
            const sorted: TodaysScheduleItem[] = items.slice();
            sorted.sort(compareTodaysScheduleItems);
            return sorted;
        }, [items]);

    return (
        <SurfaceCard>
            <div className="border-b border-gray-200 dark:border-[#262626] px-6 py-4 flex items-center justify-between">
                <div>
                    <h2 className="text-brand-dark dark:text-white">
                        Today's Schedule
                    </h2>
                    <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
                        {dateLabel}
                    </p>
                </div>
                <button
                    onClick={() => navigate('/classes')}
                    className="text-sm text-brand hover:text-brand-dark dark:text-[#8fb55e] dark:hover:text-white transition-colors"
                >
                    View all classes &rarr;
                </button>
            </div>
            <div className="p-6">
                {todayClasses.length > 0 ? (
                    <div className="space-y-3">
                        {todayClasses.map((item: TodaysScheduleItem) => {
                            return (
                                <div
                                    key={`${item.cohort_id}-${item.event_id}-${item.date}-${item.start_time}`}
                                    className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 p-4 bg-surface-hover dark:bg-[#262626] rounded-lg hover:bg-gray-100 dark:hover:bg-[#262626]/80 transition-colors group"
                                >
                                    <div
                                        onClick={() =>
                                            navigate(
                                                '/program-classes/' +
                                                    item.cohort_id
                                            )
                                        }
                                        className="flex items-center gap-4 flex-1 cursor-pointer min-w-0"
                                    >
                                        <div className="flex items-center gap-2 min-w-20 shrink-0">
                                            <Clock className="size-4 text-gray-500 dark:text-gray-400" />
                                            <span className="text-sm text-brand-dark dark:text-white">
                                                {formatTime12h(item.start_time)}
                                            </span>
                                        </div>
                                        <div className="flex-1 min-w-0">
                                            <div className="text-brand-dark dark:text-white group-hover:text-brand transition-colors truncate">
                                                {item.class_name}
                                            </div>
                                            <div className="text-sm text-gray-600 dark:text-gray-400 truncate">
                                                {item.instructor_name}
                                            </div>
                                        </div>
                                        <div className="text-sm text-gray-500 dark:text-gray-400 min-w-40 hidden md:block">
                                            {item.room || '-'}
                                        </div>
                                    </div>
                                    {item.has_attendance ? (
                                        <Button
                                            size="sm"
                                            variant="outline"
                                            onClick={(e) => {
                                                e.stopPropagation();
                                                onNavigate(item);
                                            }}
                                            className="border-gray-300 w-full sm:w-auto"
                                        >
                                            Edit Attendance
                                        </Button>
                                    ) : item.enrolled_count === 0 ? (
                                        <span className="text-sm italic text-gray-500 dark:text-gray-400 w-full sm:w-auto sm:text-right shrink-0">
                                            No residents enrolled
                                        </span>
                                    ) : (
                                        <Button
                                            size="sm"
                                            variant="default"
                                            onClick={(e) => {
                                                e.stopPropagation();
                                                onNavigate(item);
                                            }}
                                            className="bg-brand hover:bg-brand-dark text-white w-full sm:w-auto"
                                        >
                                            Take Attendance
                                        </Button>
                                    )}
                                </div>
                            );
                        })}
                    </div>
                ) : (
                    <div className="text-center py-12 text-gray-500 dark:text-gray-400">
                        <Calendar className="size-12 mx-auto mb-[15px] text-gray-300 dark:text-gray-600" />
                        <p>
                            {isLoading
                                ? "Loading today's schedule..."
                                : 'No classes scheduled for today'}
                        </p>
                    </div>
                )}
            </div>
        </SurfaceCard>
    );
}

function compareTodaysScheduleItems(
    a: TodaysScheduleItem,
    b: TodaysScheduleItem
): number {
    if (a.date === b.date) {
        return a.start_time.localeCompare(b.start_time);
    }
    return a.date.localeCompare(b.date);
}

function MissingAttendanceWidget({
    items,
    isLoading,
    variant
}: {
    items: MissingAttendanceItem[];
    isLoading: boolean;
    variant: 'department' | 'facility';
}) {
    const [showAll, setShowAll] = useState(false);
    const navigate = useNavigate();

    const missingAttendance = useMemo(() => items, [items]);

    const sliceCount = 3;
    const displayList = showAll
        ? missingAttendance
        : missingAttendance.slice(0, sliceCount);
    const remainingCount = missingAttendance.length - sliceCount;

    const isDepartment = variant === 'department';

    return (
        <SurfaceCard className="p-6">
            <div className="flex items-center gap-2 mb-4">
                <AlertCircle className="size-5 text-brand-gold" />
                <h3 className="text-brand-dark dark:text-white">
                    Missing Attendance
                </h3>
            </div>

            {missingAttendance.length > 0 ? (
                <>
                    <p className="text-sm text-gray-600 dark:text-gray-400 mb-[15px]">
                        {isDepartment
                            ? 'Past 3 days - across all facilities'
                            : 'Past 3 days'}
                    </p>
                    <div className="space-y-3">
                        {displayList.map((item: MissingAttendanceItem) => {
                            const dayLabel = new Date(
                                `${item.date}T00:00:00`
                            ).toLocaleDateString('en-US', {
                                weekday: 'long'
                            });
                            return (
                                <div
                                    key={`${item.cohort_id}-${item.date}-${item.event_id}`}
                                    className={cn(
                                        'p-3 bg-amber-50 dark:bg-[#262626] border border-amber-200 dark:border-brand-gold/30 rounded-lg',
                                        isDepartment
                                            ? 'hover:bg-amber-100 dark:hover:bg-[#262626]/80 transition-colors cursor-pointer'
                                            : undefined
                                    )}
                                    onClick={() => {
                                        if (isDepartment) {
                                            navigate(
                                                '/program-classes/' +
                                                    item.cohort_id +
                                                    '/detail'
                                            );
                                        }
                                    }}
                                >
                                    {isDepartment && (
                                        <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">
                                            {item.facility_name}
                                        </div>
                                    )}
                                    <div className="text-sm text-brand-dark dark:text-brand-gold mb-1">
                                        {item.class_name}
                                    </div>
                                    <div className="text-xs text-gray-600 dark:text-gray-400 mb-2">
                                        {dayLabel} -{' '}
                                        {formatTime12h(item.start_time)}
                                    </div>
                                    {!isDepartment && (
                                        <Button
                                            size="sm"
                                            variant="outline"
                                            onClick={() =>
                                                navigate(
                                                    '/program-classes/' +
                                                        item.cohort_id +
                                                        '/events/' +
                                                        item.event_id +
                                                        '/attendance/' +
                                                        item.date
                                                )
                                            }
                                            className="w-full text-xs border-brand-gold text-brand-gold hover:bg-brand-gold hover:text-white dark:border-brand-gold dark:text-brand-gold"
                                        >
                                            Take Attendance
                                        </Button>
                                    )}
                                </div>
                            );
                        })}
                    </div>
                    {!showAll && remainingCount > 0 && (
                        <button
                            onClick={() => setShowAll(true)}
                            className="w-full mt-3 py-2 text-sm text-brand hover:text-brand-dark dark:text-[#8fb55e] dark:hover:text-white hover:bg-surface-hover dark:hover:bg-[#262626] rounded-lg transition-colors flex items-center justify-center gap-2"
                        >
                            Show {remainingCount} more{' '}
                            <ChevronDown className="size-4" />
                        </button>
                    )}
                    {showAll && remainingCount > 0 && (
                        <button
                            onClick={() => setShowAll(false)}
                            className="w-full mt-3 py-2 text-sm text-brand hover:text-brand-dark dark:text-[#8fb55e] dark:hover:text-white hover:bg-surface-hover dark:hover:bg-[#262626] rounded-lg transition-colors flex items-center justify-center gap-2"
                        >
                            Show less <ChevronUp className="size-4" />
                        </button>
                    )}
                </>
            ) : isLoading ? (
                <div className="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">
                    Loading missing attendance...
                </div>
            ) : (
                <div className="text-sm text-gray-500 dark:text-gray-400 py-4">
                    <CheckCircle className="size-8 mx-auto mb-2 text-brand dark:text-[#8fb55e]" />
                    <p className="text-center">All attendance up to date</p>
                </div>
            )}
        </SurfaceCard>
    );
}

function QuickActions({ navigate }: { navigate: (path: string) => void }) {
    return (
        <SurfaceCard className="p-6">
            <h3 className="text-brand-dark dark:text-white mb-4">
                Quick Actions
            </h3>
            <div className="space-y-3">
                <Tooltip>
                    <TooltipTrigger asChild>
                        <button
                            onClick={() => navigate('/programs')}
                            className="w-full flex items-center gap-3 bg-white dark:bg-[#262626] hover:bg-surface-hover dark:hover:bg-[#262626]/80 text-brand-dark dark:text-white px-4 py-3 rounded-lg border border-gray-200 dark:border-[#262626] transition-colors cursor-pointer"
                        >
                            <RectangleStackIcon className="size-5 text-brand dark:text-[#8fb55e]" />
                            <span className="text-sm">View All Programs</span>
                        </button>
                    </TooltipTrigger>
                    <TooltipContent className="bg-gray-900 text-white">
                        <p>Browse all programs at your facility</p>
                    </TooltipContent>
                </Tooltip>

                <Tooltip>
                    <TooltipTrigger asChild>
                        <button
                            onClick={() => navigate('/classes')}
                            className="w-full flex items-center gap-3 bg-white dark:bg-[#262626] hover:bg-surface-hover dark:hover:bg-[#262626]/80 text-brand-dark dark:text-white px-4 py-3 rounded-lg border border-gray-200 dark:border-[#262626] transition-colors cursor-pointer"
                        >
                            <ListBulletIcon className="size-5 text-brand dark:text-[#8fb55e]" />
                            <span className="text-sm">Browse Classes</span>
                        </button>
                    </TooltipTrigger>
                    <TooltipContent className="bg-gray-900 text-white">
                        <p>View and manage all classes</p>
                    </TooltipContent>
                </Tooltip>
            </div>
        </SurfaceCard>
    );
}

function FacilityHealthTable({ rows }: { rows: FacilityHealthSummary[] }) {
    if (rows.length === 0) {
        return (
            <SurfaceCard className="p-6">
                <p className="text-gray-600 dark:text-gray-400 text-sm text-center py-4">
                    No facility data available.
                </p>
            </SurfaceCard>
        );
    }

    return (
        <SurfaceCard className="overflow-hidden">
            <table className="w-full">
                <thead className="bg-surface-hover dark:bg-[#262626] border-b border-gray-200 dark:border-[#262626]">
                    <tr>
                        <th className="text-left px-6 py-3 text-sm text-brand-dark dark:text-white">
                            Facility
                        </th>
                        <th className="text-left px-6 py-3 text-sm text-brand-dark dark:text-white">
                            Programs
                        </th>
                        <th className="text-left px-6 py-3 text-sm text-brand-dark dark:text-white">
                            Active Classes
                        </th>
                        <th className="text-left px-6 py-3 text-sm text-brand-dark dark:text-white">
                            Enrollment
                        </th>
                        <th className="text-left px-6 py-3 text-sm text-brand-dark dark:text-white">
                            <div className="flex items-center gap-1">
                                Missing Attendance
                                <InfoTooltip iconClassName="size-3.5">
                                    <p>
                                        Classes where attendance hasn't been
                                        recorded yet (past 3 days)
                                    </p>
                                </InfoTooltip>
                            </div>
                        </th>
                        <th className="text-left px-6 py-3 text-sm text-brand-dark dark:text-white">
                            <div className="flex items-center gap-1">
                                Attendance Concerns
                                <InfoTooltip iconClassName="size-3.5">
                                    <p>
                                        Classes with patterns of high resident
                                        absences (engagement issue)
                                    </p>
                                </InfoTooltip>
                            </div>
                        </th>
                    </tr>
                </thead>
                <tbody className="divide-y divide-gray-200 dark:divide-[#262626]">
                    {rows.map((row) => (
                        <tr
                            key={row.facility_id}
                            className="hover:bg-surface-hover/50 dark:hover:bg-[#262626]/50 transition-colors"
                        >
                            <td className="px-6 py-4">
                                <div className="text-brand-dark dark:text-white">
                                    {row.facility_name}
                                </div>
                            </td>
                            <td className="px-6 py-4">
                                <div className="text-sm text-gray-700 dark:text-gray-400">
                                    {row.programs}
                                </div>
                            </td>
                            <td className="px-6 py-4">
                                <div className="text-sm text-gray-700 dark:text-gray-400">
                                    {row.active_classes}
                                </div>
                            </td>
                            <td className="px-6 py-4">
                                <div className="text-sm text-gray-700 dark:text-gray-400">
                                    {row.enrollment}
                                </div>
                            </td>
                            <td className="px-6 py-4">
                                {row.missing_attendance > 0 ? (
                                    <div className="flex items-center gap-2">
                                        <span className="text-sm text-brand-gold">
                                            {row.missing_attendance}
                                        </span>
                                        <span className="text-xs text-gray-500">
                                            classes
                                        </span>
                                    </div>
                                ) : (
                                    <div className="flex items-center gap-1">
                                        <CheckCircle className="size-4 text-brand dark:text-[#8fb55e]" />
                                        <span className="text-xs text-gray-500">
                                            Up to date
                                        </span>
                                    </div>
                                )}
                            </td>
                            <td className="px-6 py-4">
                                {row.attendance_concerns > 0 ? (
                                    <div className="flex items-center gap-2">
                                        <span className="text-sm text-amber-600 dark:text-brand-gold">
                                            {row.attendance_concerns}
                                        </span>
                                        <span className="text-xs text-gray-500">
                                            classes
                                        </span>
                                    </div>
                                ) : (
                                    <div className="flex items-center gap-1">
                                        <CheckCircle className="size-4 text-brand dark:text-[#8fb55e]" />
                                        <span className="text-xs text-gray-500">
                                            None
                                        </span>
                                    </div>
                                )}
                            </td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </SurfaceCard>
    );
}

function MetricCards({
    stats,
    onNavigate
}: {
    stats: DashboardStats;
    onNavigate: (path: string) => void;
}) {
    return (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            <MetricCard
                icon={
                    <AcademicCapIcon className="size-5 text-brand dark:text-[#8fb55e]" />
                }
                iconBg="bg-surface-hover dark:bg-[#262626]"
                label="Active Classes"
                value={stats.activeClasses}
                tooltip="Classes currently in Active status with enrolled residents"
                subtitle={`${stats.scheduledClasses} scheduled`}
            />
            <MetricCard
                icon={
                    <UsersIcon className="size-5 text-brand dark:text-[#8fb55e]" />
                }
                iconBg="bg-surface-hover dark:bg-[#262626]"
                label="Total Enrollment"
                value={stats.totalEnrollment}
                tooltip="Number of residents enrolled across all active classes"
                subtitle="Across all programs"
            />
            <MetricCard
                icon={
                    <ChartBarIcon className="size-5 text-brand dark:text-[#8fb55e]" />
                }
                iconBg="bg-surface-hover dark:bg-[#262626]"
                label="Capacity Utilization"
                value={`${stats.capacityUtilization}%`}
                tooltip="Percentage of available seats filled across all classes"
                subtitle={`${stats.totalEnrollment} of ${stats.totalCapacity} seats`}
            />
            <MetricCard
                icon={<AlertCircle className="size-5 text-brand-gold" />}
                iconBg="bg-amber-50 dark:bg-[#262626]"
                label="Attendance Concerns"
                value={stats.attendanceConcerns}
                tooltip="Classes with patterns of high absences (3+ unexcused per week)"
                subtitle="High absence patterns"
                tooltipAction={
                    <button
                        onClick={() => onNavigate('/classes')}
                        className="text-brand-gold hover:text-brand-gold-dark flex items-center gap-1 text-xs"
                    >
                        View classes <ExternalLink className="size-3" />
                    </button>
                }
            />
        </div>
    );
}
