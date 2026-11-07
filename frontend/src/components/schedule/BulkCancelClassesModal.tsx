import { useState, useMemo } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { toast } from 'sonner';
import useSWR, { KeyedMutator } from 'swr';
import API from '@/api/api';
import { useAuth } from '@/auth/useAuth';
import { CancelEventReason, Cohort, ServerResponseMany } from '@/types';
import {
    Instructor,
    InstructorClassData,
    BulkCancelSessionsRequest,
    BulkCancelSessionsResponse
} from '@/types/events';
import {
    bulkCancelClassesSchema,
    BulkCancelClassesInput
} from '@/lib/validation';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import { Textarea } from '@/components/ui/textarea';
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage
} from '@/components/ui/form';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import { AlertCircle, Clock, MapPin, Users, XCircle } from 'lucide-react';
import {
    formatTime12h,
    timeToMinutes,
    parseDurationToMs
} from '@/lib/formatters';
import { FormModal, TonedPanel } from '@/components/shared';

interface BulkCancelClassesModalProps {
    open: boolean;
    onClose: () => void;
    mutate: KeyedMutator<ServerResponseMany<Cohort>>;
}

interface PreviewData {
    sessionCount: number;
    classCount: number;
    studentCount: number;
    classes: {
        classId: number;
        className: string;
        upcomingSessions: number;
        cancelledSessions: number;
        studentCount: number;
        startTime: string;
        duration: string;
        room: string;
        sessionDates: string[];
    }[];
}

function formatTimeRange(startTime: string, duration: string): string {
    if (!startTime) return '';
    const start = formatTime12h(startTime);
    if (!duration) return start;
    const durationMs = parseDurationToMs(duration);
    if (durationMs <= 0) return start;
    const startMinutes = timeToMinutes(startTime);
    const totalMinutes = startMinutes + durationMs / 60000;
    const endH = String(Math.floor(totalMinutes / 60) % 24).padStart(2, '0');
    const endM = String(Math.floor(totalMinutes % 60)).padStart(2, '0');
    return `${start} - ${formatTime12h(`${endH}:${endM}`)}`;
}

export function BulkCancelClassesModal({
    open,
    onClose,
    mutate
}: BulkCancelClassesModalProps) {
    const { user } = useAuth();
    const facilityId = user?.facility?.id;

    const form = useForm<BulkCancelClassesInput>({
        resolver: zodResolver(bulkCancelClassesSchema),
        defaultValues: {
            instructorId: '',
            startDate: '',
            endDate: '',
            reason: '',
            customReason: ''
        }
    });
    const selectedInstructor = form.watch('instructorId');
    const startDate = form.watch('startDate');
    const endDate = form.watch('endDate');
    const reason = form.watch('reason');
    const customReason = form.watch('customReason') ?? '';
    const dateRange = { start: startDate, end: endDate };
    const [loading, setLoading] = useState(false);
    const [showConfirmation, setShowConfirmation] = useState(false);
    const [preview, setPreview] = useState<PreviewData | null>(null);

    const { data: instructorsResp } = useSWR<ServerResponseMany<Instructor>>(
        open && facilityId
            ? `/api/program-facilities/${facilityId}/instructors`
            : null
    );
    const instructors = useMemo(() => {
        const list = instructorsResp?.data ?? [];
        return list.filter((i) => i.id !== 0);
    }, [instructorsResp]);

    const previewUrl =
        selectedInstructor && dateRange.start && dateRange.end
            ? `/api/instructors/${selectedInstructor}/classes?start_date=${dateRange.start}&end_date=${dateRange.end}`
            : null;
    const { data: previewResp } =
        useSWR<ServerResponseMany<InstructorClassData>>(previewUrl);
    const inlinePreview = useMemo(() => {
        const classes = previewResp?.data ?? [];
        const totalSessions = classes.reduce(
            (sum, c) => sum + c.upcomingSessions,
            0
        );
        const totalClasses = classes.filter(
            (c) => c.upcomingSessions > 0
        ).length;
        return { totalSessions, totalClasses };
    }, [previewResp]);

    const isOther =
        reason === (CancelEventReason['Other (add note)'] as string);
    const finalReason = isOther ? customReason.trim() : reason;

    function resetState() {
        form.reset({
            instructorId: '',
            startDate: '',
            endDate: '',
            reason: '',
            customReason: ''
        });
        setShowConfirmation(false);
        setPreview(null);
        setLoading(false);
    }

    function handleClose() {
        resetState();
        onClose();
    }

    async function handlePreview() {
        setLoading(true);

        try {
            const resp = await API.get<InstructorClassData[]>(
                `instructors/${selectedInstructor}/classes?start_date=${dateRange.start}&end_date=${dateRange.end}`
            );

            if (!resp.success) {
                toast.error(resp.message ?? 'Failed to fetch session data');
                return;
            }

            const classes: InstructorClassData[] = Array.isArray(resp.data)
                ? (resp.data as InstructorClassData[])
                : [];
            const upcomingClasses = classes.filter(
                (c) => c.upcomingSessions > 0
            );
            const totalUpcoming = classes.reduce(
                (sum, c) => sum + c.upcomingSessions,
                0
            );

            if (totalUpcoming === 0) {
                toast.error(
                    'No sessions found to cancel for the selected instructor and date range.'
                );
                return;
            }

            setPreview({
                sessionCount: totalUpcoming,
                classCount: upcomingClasses.length,
                studentCount: classes.reduce(
                    (sum, c) => sum + c.enrolledCount,
                    0
                ),
                classes: classes.map((c) => ({
                    classId: c.id,
                    className: c.name,
                    upcomingSessions: c.upcomingSessions,
                    cancelledSessions: c.cancelledSessions,
                    studentCount: c.enrolledCount,
                    startTime: c.startTime,
                    duration: c.duration,
                    room: c.room,
                    sessionDates: c.sessionDates ?? []
                }))
            });
            setShowConfirmation(true);
        } catch {
            toast.error('Failed to fetch session data');
        } finally {
            setLoading(false);
        }
    }

    async function handleConfirm() {
        if (!preview) return;
        toast.dismiss();
        setLoading(true);

        try {
            const request: BulkCancelSessionsRequest = {
                instructorId: Number(selectedInstructor),
                startDate: dateRange.start,
                endDate: dateRange.end,
                reason: finalReason
            };

            const resp = await API.post<
                BulkCancelSessionsResponse,
                BulkCancelSessionsRequest
            >('program-classes/bulk-cancel', request);

            if (resp.success) {
                const data = resp.data as
                    | BulkCancelSessionsResponse
                    | undefined;
                if (data?.success) {
                    const msg =
                        data.message ??
                        `Successfully cancelled ${data.sessionCount} session${data.sessionCount === 1 ? '' : 's'} across ${data.classCount} class${data.classCount === 1 ? '' : 'es'}.`;
                    toast.success(msg);
                    void mutate();
                    handleClose();
                } else {
                    toast.error(
                        'No sessions found to cancel for the selected instructor and date range.'
                    );
                }
            } else {
                toast.error(resp.message ?? 'Failed to cancel sessions');
            }
        } catch {
            toast.error('Failed to cancel sessions');
        } finally {
            setLoading(false);
        }
    }

    const instructorName = useMemo(() => {
        if (!selectedInstructor) return '';
        const inst = instructors.find(
            (i) => i.id === Number(selectedInstructor)
        );
        return inst ? `${inst.name_first} ${inst.name_last}` : '';
    }, [selectedInstructor, instructors]);

    const title = showConfirmation ? (
        <span className="flex items-center gap-2">
            <XCircle className="size-5 text-red-600" />
            Confirm Bulk Cancellation
        </span>
    ) : (
        'Cancel Classes by Instructor'
    );
    const description = showConfirmation
        ? 'Review the sessions that will be cancelled'
        : 'Cancel all class sessions for a specific instructor within a date range';

    return (
        <FormModal
            open={open}
            onOpenChange={handleClose}
            title={title}
            description={description}
            className="max-w-2xl max-h-[90vh] overflow-y-auto"
        >
            {!showConfirmation ? (
                <Form {...form}>
                    <form
                        onSubmit={(e) => {
                            e.preventDefault();
                            void form.handleSubmit(() => void handlePreview())(
                                e
                            );
                        }}
                    >
                        <div className="space-y-5 py-4">
                            <FormField
                                control={form.control}
                                name="instructorId"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel className="text-sm font-medium text-brand-dark">
                                            Instructor{' '}
                                            <span className="text-destructive">
                                                *
                                            </span>
                                        </FormLabel>
                                        <Select
                                            value={field.value}
                                            onValueChange={field.onChange}
                                        >
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder="Select an instructor" />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent>
                                                {instructors.map((inst) => (
                                                    <SelectItem
                                                        key={inst.id}
                                                        value={String(inst.id)}
                                                    >
                                                        {inst.name_first}{' '}
                                                        {inst.name_last}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <div>
                                <Label className="text-sm font-medium text-brand-dark mb-3 block">
                                    Date Range{' '}
                                    <span className="text-destructive">*</span>
                                </Label>
                                <div className="flex gap-3">
                                    <FormField
                                        control={form.control}
                                        name="startDate"
                                        render={({ field }) => (
                                            <FormItem className="flex-1 space-y-1">
                                                <FormLabel className="text-xs text-gray-600">
                                                    From
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        type="date"
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={form.control}
                                        name="endDate"
                                        render={({ field }) => (
                                            <FormItem className="flex-1 space-y-1">
                                                <FormLabel className="text-xs text-gray-600">
                                                    To
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        type="date"
                                                        min={startDate}
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                            </div>

                            <FormField
                                control={form.control}
                                name="reason"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel className="text-sm font-medium text-brand-dark">
                                            Cancellation Reason{' '}
                                            <span className="text-destructive">
                                                *
                                            </span>
                                        </FormLabel>
                                        <Select
                                            value={field.value}
                                            onValueChange={field.onChange}
                                        >
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder="Select a reason" />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent>
                                                {Object.values(
                                                    CancelEventReason
                                                ).map((r) => (
                                                    <SelectItem
                                                        key={r}
                                                        value={r}
                                                    >
                                                        {r}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            {isOther && (
                                <FormField
                                    control={form.control}
                                    name="customReason"
                                    render={({ field }) => (
                                        <FormItem className="space-y-2">
                                            <FormLabel className="text-sm font-medium text-brand-dark">
                                                Details{' '}
                                                <span className="text-destructive">
                                                    *
                                                </span>
                                            </FormLabel>
                                            <FormControl>
                                                <Textarea
                                                    placeholder="Please provide details for the cancellation..."
                                                    rows={3}
                                                    {...field}
                                                />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            )}

                            {selectedInstructor &&
                                dateRange.start &&
                                dateRange.end && (
                                    <TonedPanel tone="blue">
                                        <div className="flex items-start gap-2">
                                            <AlertCircle className="size-4 text-blue-700 mt-0.5 flex-shrink-0" />
                                            <div className="text-sm text-blue-900">
                                                <strong>Preview:</strong> This
                                                will affect{' '}
                                                <strong>
                                                    {
                                                        inlinePreview.totalSessions
                                                    }
                                                </strong>{' '}
                                                {inlinePreview.totalSessions ===
                                                1
                                                    ? 'session'
                                                    : 'sessions'}{' '}
                                                across{' '}
                                                <strong>
                                                    {inlinePreview.totalClasses}
                                                </strong>{' '}
                                                {inlinePreview.totalClasses ===
                                                1
                                                    ? 'class'
                                                    : 'classes'}
                                                .
                                            </div>
                                        </div>
                                    </TonedPanel>
                                )}

                            <div className="flex gap-2 justify-end pt-4 border-t">
                                <Button
                                    variant="outline"
                                    type="button"
                                    onClick={handleClose}
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    disabled={loading}
                                    variant="brand"
                                >
                                    {loading
                                        ? 'Loading...'
                                        : 'Preview Cancellations'}
                                </Button>
                            </div>
                        </div>
                    </form>
                </Form>
            ) : (
                <div className="space-y-5 py-4">
                    <TonedPanel tone="red">
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between">
                                <span className="text-gray-600">
                                    Instructor:
                                </span>
                                <span className="font-medium text-brand-dark">
                                    {instructorName}
                                </span>
                            </div>
                            <div className="flex justify-between">
                                <span className="text-gray-600">
                                    Date Range:
                                </span>
                                <span className="font-medium text-brand-dark">
                                    {new Date(
                                        dateRange.start
                                    ).toLocaleDateString('en-US', {
                                        month: 'short',
                                        day: 'numeric',
                                        year: 'numeric'
                                    })}{' '}
                                    -{' '}
                                    {new Date(dateRange.end).toLocaleDateString(
                                        'en-US',
                                        {
                                            month: 'short',
                                            day: 'numeric',
                                            year: 'numeric'
                                        }
                                    )}
                                </span>
                            </div>
                            <div className="flex justify-between">
                                <span className="text-gray-600">Reason:</span>
                                <span className="font-medium text-brand-dark">
                                    {finalReason}
                                </span>
                            </div>
                            <div className="flex justify-between pt-2 border-t border-red-300">
                                <span className="text-gray-600">
                                    Total Sessions:
                                </span>
                                <span className="font-bold text-red-700">
                                    {preview?.sessionCount ?? 0}
                                </span>
                            </div>
                            <div className="flex justify-between">
                                <span className="text-gray-600">
                                    Total Classes Affected:
                                </span>
                                <span className="font-bold text-red-700">
                                    {preview?.classCount ?? 0}
                                </span>
                            </div>
                        </div>
                    </TonedPanel>

                    {preview && preview.classes.length > 0 && (
                        <div className="space-y-3">
                            <Label className="text-sm font-medium text-brand-dark">
                                Affected Classes ({preview.classCount})
                            </Label>
                            <div className="max-h-80 overflow-y-auto border border-gray-200 rounded-md">
                                <div className="divide-y divide-gray-200">
                                    {preview.classes
                                        .filter((c) => c.upcomingSessions > 0)
                                        .map((cls) => (
                                            <div
                                                key={cls.classId}
                                                className="p-4 hover:bg-gray-50"
                                            >
                                                <div className="flex items-start justify-between mb-3">
                                                    <h4 className="font-medium text-brand-dark">
                                                        {cls.className}
                                                    </h4>
                                                    <Badge className="bg-red-100 text-red-700 border-red-300 flex-shrink-0">
                                                        {cls.upcomingSessions}{' '}
                                                        {cls.upcomingSessions ===
                                                        1
                                                            ? 'session'
                                                            : 'sessions'}
                                                    </Badge>
                                                </div>
                                                <div className="space-y-1.5 text-xs text-gray-600">
                                                    {cls.startTime && (
                                                        <div className="flex items-center gap-1.5">
                                                            <Clock className="size-3.5 flex-shrink-0" />
                                                            <span>
                                                                {formatTimeRange(
                                                                    cls.startTime,
                                                                    cls.duration
                                                                )}
                                                            </span>
                                                        </div>
                                                    )}
                                                    {cls.room && (
                                                        <div className="flex items-center gap-1.5">
                                                            <MapPin className="size-3.5 flex-shrink-0" />
                                                            <span>
                                                                {cls.room}
                                                            </span>
                                                        </div>
                                                    )}
                                                    <div className="flex items-center gap-1.5">
                                                        <Users className="size-3.5 flex-shrink-0" />
                                                        <span>
                                                            {cls.studentCount}{' '}
                                                            residents enrolled
                                                        </span>
                                                    </div>
                                                </div>
                                                {cls.sessionDates.length >
                                                    0 && (
                                                    <div className="flex flex-wrap gap-1 mt-3">
                                                        {cls.sessionDates
                                                            .slice(0, 5)
                                                            .map((date) => (
                                                                <span
                                                                    key={date}
                                                                    className="text-xs bg-gray-100 text-gray-700 px-2 py-1 rounded"
                                                                >
                                                                    {new Date(
                                                                        date +
                                                                            'T00:00:00'
                                                                    ).toLocaleDateString(
                                                                        'en-US',
                                                                        {
                                                                            month: 'short',
                                                                            day: 'numeric'
                                                                        }
                                                                    )}
                                                                </span>
                                                            ))}
                                                        {cls.sessionDates
                                                            .length > 5 && (
                                                            <span className="text-xs text-gray-500 px-2 py-1">
                                                                +
                                                                {cls
                                                                    .sessionDates
                                                                    .length -
                                                                    5}{' '}
                                                                more
                                                            </span>
                                                        )}
                                                    </div>
                                                )}
                                            </div>
                                        ))}
                                </div>
                            </div>
                        </div>
                    )}

                    <TonedPanel tone="yellow">
                        <div className="flex items-start gap-2">
                            <AlertCircle className="size-4 text-yellow-700 mt-0.5 flex-shrink-0" />
                            <div className="text-sm text-yellow-900">
                                <strong>Warning:</strong> This action will
                                cancel all selected sessions. Residents will
                                need to be notified of these cancellations.
                            </div>
                        </div>
                    </TonedPanel>

                    <div className="flex gap-2 justify-end pt-4 border-t">
                        <Button
                            variant="outline"
                            onClick={() => setShowConfirmation(false)}
                        >
                            Back
                        </Button>
                        <Button
                            onClick={() => void handleConfirm()}
                            disabled={loading}
                            className="bg-red-600 hover:bg-red-700 text-white"
                        >
                            <XCircle className="size-4 mr-2" />
                            {loading ? 'Cancelling...' : 'Confirm Cancellation'}
                        </Button>
                    </div>
                </div>
            )}
        </FormModal>
    );
}
