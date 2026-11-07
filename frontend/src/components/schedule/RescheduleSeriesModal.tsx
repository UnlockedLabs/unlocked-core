import { useEffect, useRef, useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { toast } from 'sonner';
import { AlertCircle } from 'lucide-react';
import API from '@/api/api';
import { FacilityProgramClassEvent, Room, RoomConflict } from '@/types';
import { useInstructors } from '@/hooks/useInstructors';
import {
    rescheduleSeriesSchema,
    RescheduleSeriesInput
} from '@/lib/validation';
import { FormModal } from '@/components/shared/FormModal';
import { RoomConflictModal } from './RoomConflictModal';
import { RRuleControl, RRuleFormHandle } from './RRuleControl';
import { Button } from '@/components/ui/button';
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

interface RescheduleSeriesModalProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    event: FacilityProgramClassEvent;
    rooms: Room[];
    facilityId: string;
    onSuccess: () => void;
}

function parseRRuleDefaults(event: FacilityProgramClassEvent) {
    const rule = event.recurrence_rule ?? '';
    let startTime = '';
    let endTime = '';
    let days: string[] = [];
    let endDate = '';

    const dtMatch = /T(\d{2})(\d{2})/.exec(rule);
    if (dtMatch) {
        startTime = `${dtMatch[1]}:${dtMatch[2]}`;
    }

    if (startTime && event.duration) {
        const durMatch = /(\d+)h(\d+)m/.exec(event.duration);
        if (durMatch) {
            const [, hours, mins] = durMatch;
            const [sh, sm] = startTime.split(':').map(Number);
            const totalMin =
                (sh ?? 0) * 60 + (sm ?? 0) + Number(hours) * 60 + Number(mins);
            const eh = Math.floor(totalMin / 60) % 24;
            const em = totalMin % 60;
            endTime = `${String(eh).padStart(2, '0')}:${String(em).padStart(2, '0')}`;
        }
    }

    const byDayMatch = /BYDAY=([A-Z,]+)/.exec(rule);
    if (byDayMatch) {
        days = byDayMatch[1].split(',');
    }

    const untilMatch = /UNTIL=(\d{4})(\d{2})(\d{2})/.exec(rule);
    if (untilMatch) {
        endDate = `${untilMatch[1]}-${untilMatch[2]}-${untilMatch[3]}`;
    }

    return { startTime, endTime, days, endDate };
}

function closedSeriesRRule(originalRule: string, closeDateStr: string): string {
    const untilStr = closeDateStr.replace(/-/g, '') + 'T235959Z';
    const ruleClean = originalRule
        .replace(/;UNTIL=\d{8}T\d{6}Z/, '')
        .replace(/\n/, '\n');
    const parts = ruleClean.split('\n');
    return parts[0] + '\n' + parts[1] + `;UNTIL=${untilStr}`;
}

function dayBefore(dateStr: string): string {
    const d = new Date(dateStr + 'T00:00:00');
    d.setDate(d.getDate() - 1);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

export function RescheduleSeriesModal({
    open,
    onOpenChange,
    event,
    rooms,
    facilityId,
    onSuccess
}: RescheduleSeriesModalProps) {
    const rruleRef = useRef<RRuleFormHandle>(null);
    const form = useForm<RescheduleSeriesInput>({
        resolver: zodResolver(rescheduleSeriesSchema),
        defaultValues: {
            room_id: event.room_id ? String(event.room_id) : '',
            instructor_id: event.instructor_id
                ? String(event.instructor_id)
                : ''
        }
    });
    const roomId = form.watch('room_id') ?? '';
    const instructorId = form.watch('instructor_id') ?? '';
    const [submitting, setSubmitting] = useState(false);
    const [conflicts, setConflicts] = useState<RoomConflict[]>([]);
    const [showConflicts, setShowConflicts] = useState(false);

    const instructors = useInstructors(facilityId, open);

    useEffect(() => {
        if (open) {
            form.reset({
                room_id: event.room_id ? String(event.room_id) : '',
                instructor_id: event.instructor_id
                    ? String(event.instructor_id)
                    : ''
            });
        }
    }, [open, event, form]);

    const defaults = parseRRuleDefaults(event);
    const currentRoomName =
        rooms.find((r) => r.id === event.room_id)?.name ?? '';

    const DAY_CODE_MAP: Record<string, string> = {
        MO: 'Monday',
        TU: 'Tuesday',
        WE: 'Wednesday',
        TH: 'Thursday',
        FR: 'Friday',
        SA: 'Saturday',
        SU: 'Sunday'
    };
    const currentDays = defaults.days
        .map((d) => DAY_CODE_MAP[d] ?? d)
        .join(', ');
    const currentTimeRange =
        defaults.startTime && defaults.endTime
            ? `${defaults.startTime} - ${defaults.endTime}`
            : '';

    const sessionDateLong = event.start.toLocaleDateString('en-US', {
        weekday: 'long',
        month: 'long',
        day: 'numeric',
        year: 'numeric'
    });

    async function handleSubmit() {
        if (!rruleRef.current?.validate()) {
            toast.error('Please fill in all required schedule fields');
            return;
        }
        const result = rruleRef.current.createRule();
        if (!result) return;

        const newStartDate = /DTSTART;TZID=Local:(\d{4})(\d{2})(\d{2})/.exec(
            result.rule
        );
        if (!newStartDate) {
            toast.error('Invalid start date');
            return;
        }
        const newStartDateStr = `${newStartDate[1]}-${newStartDate[2]}-${newStartDate[3]}`;
        const closeDate = dayBefore(newStartDateStr);

        setSubmitting(true);
        const resp = await API.put(
            `program-classes/${event.cohort_id}/events`,
            {
                event_series: {
                    cohort_id: event.cohort_id,
                    recurrence_rule: result.rule,
                    duration: result.duration,
                    room_id: roomId ? Number(roomId) : event.room_id,
                    instructor_id: instructorId
                        ? Number(instructorId)
                        : (event.instructor_id ?? null)
                },
                closed_event_series: {
                    id: event.id,
                    cohort_id: event.cohort_id,
                    recurrence_rule: closedSeriesRRule(
                        event.recurrence_rule,
                        closeDate
                    ),
                    duration: event.duration,
                    room_id: event.room_id
                }
            }
        );
        setSubmitting(false);

        if (resp.status === 409 && Array.isArray(resp.data)) {
            setConflicts(resp.data);
            setShowConflicts(true);
            return;
        }

        if (resp.success) {
            toast.success('Class rescheduled successfully');
            onOpenChange(false);
            onSuccess();
        } else {
            toast.error(resp.message || 'Failed to reschedule series');
        }
    }

    return (
        <>
            <FormModal
                open={open}
                onOpenChange={onOpenChange}
                title="Reschedule Series"
                description={`Update the recurring schedule pattern starting from ${sessionDateLong}. This will affect all future sessions in the series.`}
                className="max-w-lg max-h-[90vh] overflow-y-auto"
                preventAutoFocus
            >
                <Form {...form}>
                    <div className="pt-4 pb-0">
                        <div className="space-y-5">
                            <div className="bg-gray-50 border border-gray-200 rounded-lg p-4">
                                <h4 className="text-sm font-medium text-gray-700 mb-2">
                                    Current Schedule
                                </h4>
                                <div className="space-y-1 text-sm text-gray-600">
                                    {currentDays && (
                                        <div>
                                            <span className="font-medium">
                                                Days:
                                            </span>{' '}
                                            {currentDays}
                                        </div>
                                    )}
                                    {currentTimeRange && (
                                        <div>
                                            <span className="font-medium">
                                                Time:
                                            </span>{' '}
                                            {currentTimeRange}
                                        </div>
                                    )}
                                    {currentRoomName && (
                                        <div>
                                            <span className="font-medium">
                                                Room:
                                            </span>{' '}
                                            {currentRoomName}
                                        </div>
                                    )}
                                    {event.instructor_name && (
                                        <div>
                                            <span className="font-medium">
                                                Instructor:
                                            </span>{' '}
                                            {event.instructor_name}
                                        </div>
                                    )}
                                </div>
                            </div>

                            <RRuleControl
                                ref={rruleRef}
                                defaultStartDate={`${event.start.getFullYear()}-${String(event.start.getMonth() + 1).padStart(2, '0')}-${String(event.start.getDate()).padStart(2, '0')}`}
                                defaultStartTime={defaults.startTime}
                                defaultEndTime={defaults.endTime}
                                defaultDays={defaults.days}
                                defaultEndDate={defaults.endDate}
                                startDateLabel="Effective Starting From *"
                                startDateHelper="Changes will apply to all sessions on or after this date"
                            />

                            <FormField
                                control={form.control}
                                name="room_id"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel>New Room *</FormLabel>
                                        <Select
                                            value={field.value}
                                            onValueChange={field.onChange}
                                        >
                                            <FormControl>
                                                <SelectTrigger>
                                                    <SelectValue placeholder="Select a room" />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent>
                                                {rooms.map((room) => (
                                                    <SelectItem
                                                        key={room.id}
                                                        value={String(room.id)}
                                                    >
                                                        {room.name}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <FormField
                                control={form.control}
                                name="instructor_id"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel>Instructor</FormLabel>
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
                                                        {inst.name_last},{' '}
                                                        {inst.name_first}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <div className="bg-amber-50 border border-amber-200 rounded-lg p-4">
                                <div className="flex gap-3">
                                    <AlertCircle className="size-5 text-amber-600 shrink-0 mt-0.5" />
                                    <div>
                                        <p className="text-sm text-amber-900 font-medium mb-1">
                                            Important
                                        </p>
                                        <p className="text-sm text-amber-700">
                                            This will update the recurring
                                            schedule for all future sessions.
                                            Sessions before the effective date
                                            will remain on the original
                                            schedule.
                                        </p>
                                    </div>
                                </div>
                            </div>
                        </div>

                        <div className="flex justify-end gap-2 mt-8">
                            <Button
                                variant="outline"
                                onClick={() => onOpenChange(false)}
                            >
                                Cancel
                            </Button>
                            <Button
                                onClick={() => void handleSubmit()}
                                disabled={submitting}
                                className="bg-brand hover:bg-brand-dark text-white"
                            >
                                {submitting ? 'Saving...' : 'Reschedule Series'}
                            </Button>
                        </div>
                    </div>
                </Form>
            </FormModal>

            <RoomConflictModal
                open={showConflicts}
                onOpenChange={setShowConflicts}
                conflicts={conflicts}
            />
        </>
    );
}
