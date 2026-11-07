import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import useSWR from 'swr';
import { ChevronRight } from 'lucide-react';
import { useAuth, isUserDeactivated, canSwitchFacility } from '@/auth/useAuth';
import {
    ServerResponseOne,
    ServerResponseMany,
    ResidentEngagementProfile,
    ResidentProgramOverview,
    EnrollmentStatus,
    Facility
} from '@/types';
import { Skeleton } from '@/components/ui/skeleton';
import { ResidentHeader } from './resident-profile/ResidentHeader';
import { ResidentMetrics } from './resident-profile/ResidentMetrics';
import { ActiveEnrollmentsTable } from './resident-profile/ActiveEnrollmentsTable';
import { AttendanceTrendChart } from './resident-profile/AttendanceTrendChart';
import { DetailedAttendanceDialog } from './resident-profile/DetailedAttendanceDialog';
import { CompletedPrograms } from './resident-profile/CompletedPrograms';
import { IncompleteEnrollments } from './resident-profile/IncompleteEnrollments';
import { HistoricalNotes } from './resident-profile/HistoricalNotes';
import ActivityHistoryCard from '@/components/student/ActivityHistoryCard';
import { AddNoteDialog } from './resident-profile/AddNoteDialog';
import {
    EditResidentDialog,
    DeactivateDialog,
    DeleteDialog,
    TransferDialog
} from '@/components/residents/ResidentDialogs';
import { ResetPasswordModal } from '@/components/shared';

export default function ResidentProfile() {
    const { user } = useAuth();
    const navigate = useNavigate();
    const { user_id: residentId } = useParams<{ user_id: string }>();

    const {
        data: profileResp,
        error,
        isLoading,
        mutate: mutateProfile
    } = useSWR<ServerResponseOne<ResidentEngagementProfile>, Error>(
        `/api/users/${residentId}/profile`
    );

    const { data: programsResp, mutate: mutatePrograms } = useSWR<
        ServerResponseMany<ResidentProgramOverview>
    >(residentId ? `/api/program-users/${residentId}/programs` : null);

    const profileData = profileResp?.data;
    const programs = useMemo(() => programsResp?.data ?? [], [programsResp]);

    const {
        activeEnrollments,
        completedPrograms,
        incompleteEnrollments,
        attendanceStats
    } = useMemo(() => {
        const active = programs.filter(
            (p) => p.enrollment_status === EnrollmentStatus.Enrolled
        );
        const completed = programs.filter(
            (p) => p.enrollment_status === EnrollmentStatus.Completed
        );
        const incomplete = programs.filter((p) =>
            p.enrollment_status?.startsWith('Incomplete:')
        );
        const totalAttended = programs.reduce(
            (sum, p) => sum + (p.present_attendance ?? 0),
            0
        );
        const totalAbsent = programs.reduce(
            (sum, p) => sum + (p.absent_attendance ?? 0),
            0
        );
        const totalSessions = totalAttended + totalAbsent;
        const overallPercent =
            totalSessions > 0
                ? Math.round((totalAttended / totalSessions) * 100)
                : 0;

        return {
            activeEnrollments: active,
            completedPrograms: completed,
            incompleteEnrollments: incomplete,
            attendanceStats: {
                totalAttended,
                totalSessions,
                overallPercent
            }
        };
    }, [programs]);

    useEffect(() => {
        if (error?.message === 'Not Found') {
            navigate('/404');
        } else if (error) {
            navigate('/error');
        }
    }, [error, navigate]);

    const { data: trendResp } = useSWR<
        ServerResponseOne<{ week: string; rate: number }[]>
    >(
        residentId
            ? `/api/program-users/${residentId}/attendance-trend?weeks=8`
            : null
    );
    const chartData = useMemo(() => trendResp?.data ?? [], [trendResp]);

    const { data: notesResp, mutate: mutateNotes } = useSWR<
        ServerResponseOne<
            { id: number; date: string; admin: string; note: string }[]
        >
    >(residentId ? `/api/users/${residentId}/notes` : null);
    const notes = useMemo(() => notesResp?.data ?? [], [notesResp]);

    const { data: facilitiesResp } =
        useSWR<ServerResponseMany<Facility>>('/api/facilities');
    const facilities = useMemo(
        () => facilitiesResp?.data ?? [],
        [facilitiesResp]
    );

    const [selectedEnrollment, setSelectedEnrollment] =
        useState<ResidentProgramOverview | null>(null);
    const [attendanceDialogOpen, setAttendanceDialogOpen] = useState(false);

    const handleViewDetails = useCallback(
        (enrollment: ResidentProgramOverview) => {
            setSelectedEnrollment(enrollment);
            setAttendanceDialogOpen(true);
        },
        []
    );

    const [editOpen, setEditOpen] = useState(false);
    const [resetPwOpen, setResetPwOpen] = useState(false);
    const [deactivateOpen, setDeactivateOpen] = useState(false);
    const [deleteOpen, setDeleteOpen] = useState(false);
    const [transferOpen, setTransferOpen] = useState(false);
    const [addNoteOpen, setAddNoteOpen] = useState(false);

    const handleActionSuccess = useCallback(() => {
        void mutateProfile();
        void mutatePrograms();
        void mutateNotes();
        setTimeout(() => {
            void mutateProfile();
            void mutatePrograms();
        }, 1000);
    }, [mutateProfile, mutatePrograms, mutateNotes]);

    if (error || !user) return null;

    if (isLoading) {
        return (
            <div className="min-h-[calc(100vh-4rem)] bg-surface-hover">
                <div className="max-w-7xl mx-auto px-6 py-8 space-y-6">
                    <Skeleton className="h-5 w-48" />
                    <Skeleton className="h-36 w-full" />
                    <div className="grid grid-cols-3 gap-4">
                        <Skeleton className="h-24" />
                        <Skeleton className="h-24" />
                        <Skeleton className="h-24" />
                    </div>
                </div>
            </div>
        );
    }

    if (!profileData) return null;

    const residentUser = profileData.user;
    const engagement = profileData.activity_engagement;
    const isDeactivated = isUserDeactivated(residentUser);
    const userIsDeptAdmin = canSwitchFacility(user);

    return (
        <div className="max-w-7xl mx-auto px-6 py-8">
            <div className="flex items-center gap-2 text-sm text-gray-600 mb-6">
                <button
                    onClick={() => navigate('/residents')}
                    className="hover:text-brand transition-colors"
                >
                    Residents
                </button>
                <ChevronRight className="size-4" />
                <span className="text-brand-dark font-medium">
                    {residentUser.name_first} {residentUser.name_last}
                </span>
            </div>

            <ResidentHeader
                user={residentUser}
                facilityName={residentUser.facility?.name ?? ''}
                joinedDate={engagement?.joined ?? ''}
                lastActiveDate={engagement?.last_active_date ?? ''}
                isDeactivated={isDeactivated}
                isDeptAdmin={userIsDeptAdmin}
                onEditProfile={() => setEditOpen(true)}
                onResetPassword={() => setResetPwOpen(true)}
                onDeactivate={() => setDeactivateOpen(true)}
                onTransfer={() => setTransferOpen(true)}
                onDelete={() => setDeleteOpen(true)}
            />

            <ResidentMetrics
                overallAttendancePercent={attendanceStats.overallPercent}
                sessionsAttended={attendanceStats.totalAttended}
                totalSessions={attendanceStats.totalSessions}
                activeEnrollments={activeEnrollments.length}
                completedPrograms={completedPrograms.length}
            />

            <AttendanceTrendChart data={chartData} />

            <ActiveEnrollmentsTable
                enrollments={activeEnrollments}
                onViewDetails={handleViewDetails}
            />

            <DetailedAttendanceDialog
                open={attendanceDialogOpen}
                onOpenChange={setAttendanceDialogOpen}
                enrollment={selectedEnrollment}
                residentId={residentId ?? ''}
            />

            <CompletedPrograms
                programs={completedPrograms}
                onViewDetails={handleViewDetails}
            />

            <IncompleteEnrollments enrollments={incompleteEnrollments} />

            <HistoricalNotes
                notes={notes}
                isDeactivated={isDeactivated}
                onAddNote={() => setAddNoteOpen(true)}
            />

            <ActivityHistoryCard residentId={residentId} />

            <EditResidentDialog
                open={editOpen}
                onOpenChange={setEditOpen}
                resident={residentUser}
                onSuccess={handleActionSuccess}
            />
            <ResetPasswordModal
                open={resetPwOpen}
                onOpenChange={setResetPwOpen}
                name={`${residentUser.name_first} ${residentUser.name_last}`}
                userId={residentUser.id}
            />
            <DeactivateDialog
                open={deactivateOpen}
                onOpenChange={setDeactivateOpen}
                resident={residentUser}
                onSuccess={handleActionSuccess}
            />
            <DeleteDialog
                open={deleteOpen}
                onOpenChange={setDeleteOpen}
                resident={residentUser}
                onSuccess={() => navigate('/residents')}
            />
            <TransferDialog
                open={transferOpen}
                onOpenChange={setTransferOpen}
                resident={residentUser}
                facilities={facilities}
                onSuccess={handleActionSuccess}
            />
            <AddNoteDialog
                open={addNoteOpen}
                onOpenChange={setAddNoteOpen}
                residentId={residentId ?? ''}
                residentName={residentUser.name_first}
                onSuccess={() => void mutateNotes()}
            />
        </div>
    );
}
