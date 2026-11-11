import { useMemo } from 'react';
import useSWR from 'swr';
import { useAuth } from '@/auth/useAuth';
import {
    ServerResponseMany,
    ResidentProgramOverview,
    EnrollmentStatus,
    ProgClassStatus
} from '@/types';
import { PageHeader, StatusBadge, EmptyState } from '@/components/shared';
import { formatDate } from '@/lib/formatters';
import { Card, CardContent } from '@/components/ui/card';
import { GraduationCap } from 'lucide-react';

function getEnrollmentStatusStyle(status?: EnrollmentStatus): string {
    switch (status) {
        case EnrollmentStatus.Enrolled:
            return 'bg-green-50 text-green-700 border-green-200';
        case EnrollmentStatus.Completed:
            return 'bg-blue-50 text-blue-700 border-blue-200';
        case EnrollmentStatus.Dropped:
            return 'bg-red-50 text-red-700 border-red-200';
        default:
            return 'bg-muted text-foreground border-border';
    }
}

export default function ResidentOverview() {
    const { user } = useAuth();

    const startDate = useMemo(() => {
        const d = new Date(Date.now() - 1000 * 60 * 60 * 24 * 90);
        return d.toISOString();
    }, []);
    const endDate = useMemo(() => {
        const d = new Date(Date.now() + 1000 * 60 * 60 * 24 * 365);
        return d.toISOString();
    }, []);

    const { data: programsResp, isLoading } = useSWR<
        ServerResponseMany<ResidentProgramOverview>
    >(user ? `/api/program-users/${user.id}/programs` : null);

    const { data: eventsResp } = useSWR<ServerResponseMany<unknown>>(
        user
            ? `/api/student-calendar?start_dt=${startDate}&end_dt=${endDate}`
            : null
    );

    if (!user) return null;

    const programs = programsResp?.data ?? [];
    const upcomingEvents = (eventsResp?.data ?? []).length;

    return (
        <div className="bg-muted min-h-screen p-6">
            <div className="max-w-7xl mx-auto space-y-6">
                <PageHeader
                    title="My Programs"
                    subtitle={`${programs.length} program${programs.length !== 1 ? 's' : ''} | ${upcomingEvents} upcoming event${upcomingEvents !== 1 ? 's' : ''}`}
                />

                {isLoading && (
                    <p className="text-muted-foreground text-center py-8">
                        Loading...
                    </p>
                )}

                {!isLoading && programs.length === 0 && (
                    <EmptyState
                        icon={
                            <GraduationCap className="size-6 text-muted-foreground" />
                        }
                        title="No programs yet"
                        description="You are not currently enrolled in any programs."
                    />
                )}

                {!isLoading && programs.length > 0 && (
                    <Card>
                        <CardContent className="p-0">
                            <div className="overflow-x-auto">
                                <table className="w-full text-sm">
                                    <thead>
                                        <tr className="border-b border-border bg-muted/50">
                                            <th className="th-muted">
                                                Program
                                            </th>
                                            <th className="th-muted">Class</th>
                                            <th className="th-muted">Status</th>
                                            <th className="th-muted">
                                                Enrollment
                                            </th>
                                            <th className="th-muted">
                                                Start Date
                                            </th>
                                            <th className="th-muted">
                                                Attendance
                                            </th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {programs.map((prog, idx) => (
                                            <tr
                                                key={idx}
                                                className="border-b border-border last:border-0 hover:bg-muted/50"
                                            >
                                                <td className="py-3 px-5 font-medium text-foreground">
                                                    {prog.program_name}
                                                </td>
                                                <td className="py-3 px-5 text-muted-foreground">
                                                    {prog.class_name}
                                                </td>
                                                <td className="py-3 px-5">
                                                    <StatusBadge
                                                        status={
                                                            prog.is_canvas
                                                                ? ProgClassStatus.ACTIVE
                                                                : prog.status
                                                        }
                                                        variant="progClass"
                                                    />
                                                </td>
                                                <td className="py-3 px-5">
                                                    {prog.enrollment_status && (
                                                        <StatusBadge
                                                            status={
                                                                prog.enrollment_status
                                                            }
                                                            className={getEnrollmentStatusStyle(
                                                                prog.enrollment_status
                                                            )}
                                                        />
                                                    )}
                                                </td>
                                                <td className="py-3 px-5 text-muted-foreground">
                                                    {formatDate(
                                                        prog.start_date
                                                    )}
                                                </td>
                                                <td className="py-3 px-5 text-muted-foreground">
                                                    {prog.is_canvas
                                                        ? '-'
                                                        : (prog.attendance_percentage ??
                                                          '-')}
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        </CardContent>
                    </Card>
                )}
            </div>
        </div>
    );
}
