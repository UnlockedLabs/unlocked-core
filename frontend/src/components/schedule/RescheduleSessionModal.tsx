import { useState, useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { toast } from 'sonner';
import API from '@/api/api';
import { useAuth } from '@/auth/useAuth';
import { FacilityProgramClassEvent, Room, RoomConflict } from '@/types';
import { useInstructors } from '@/hooks/useInstructors';
import {
    scheduleRescheduleSessionSchema,
    ScheduleRescheduleSessionInput
} from '@/lib/validation';
import { FormModal } from '@/components/shared/FormModal';
import { RoomConflictModal } from './RoomConflictModal';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
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
import { toDateInput, toTimeInput, formatDurationStr } from '@/lib/formatters';

interface RescheduleSessionModalProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    event: FacilityProgramClassEvent;
    rooms: Room[];
    facilityId: string;
    onSuccess: () => void;
}

export function RescheduleSessionModal({
    open,
    onOpenChange,
    event,
    rooms,
    facilityId,
    onSuccess
}: RescheduleSessionModalProps) {
    const { user } = useAuth();
    const form = useForm<ScheduleRescheduleSessionInput>({
        resolver: zodResolver(scheduleRescheduleSessionSchema),
        defaultValues: {
            date: '',
            startTime: '',
            endTime: '',
            room_id: '',
            instructor_id: ''
        }
    });
    const [submitting, setSubmitting] = useState(false);
    const [conflicts, setConflicts] = useState<RoomConflict[]>([]);
    const [showConflicts, setShowConflicts] = useState(false);

    const currentRoomName =
        rooms.find((r) => r.id === event.room_id)?.name ?? '';

    const instructors = useInstructors(facilityId, open);

    useEffect(() => {
        if (open && event) {
            form.reset({
                date: toDateInput(event.start),
                startTime: '',
                endTime: '',
                room_id: '',
                instructor_id: event.instructor_id
                    ? String(event.instructor_id)
                    : ''
            });
        }
    }, [open, event, form]);

    async function handleSubmit(formData: ScheduleRescheduleSessionInput) {
        if (!user) return;

        const effectiveStart = formData.startTime || toTimeInput(event.start);
        const effectiveEnd = formData.endTime || toTimeInput(event.end);
        const duration = formatDurationStr(effectiveStart, effectiveEnd);
        if (duration === '0h0m0s') {
            toast.error('End time must be after start time');
            return;
        }

        setSubmitting(true);
        const origDt = `${toDateInput(event.start).replace(/-/g, '')}T${toTimeInput(event.start).replace(':', '')}00`;
        const newDt = `${formData.date.replace(/-/g, '')}T${effectiveStart.replace(':', '')}00`;

        const resp = await API.put(
            `program-classes/${event.cohort_id}/events/${event.id}`,
            [
                {
                    event_id: event.id,
                    is_cancelled: true,
                    override_rrule: `DTSTART;TZID=${user.timezone}:${origDt}\nRRULE:FREQ=DAILY;COUNT=1`,
                    duration: event.duration,
                    reason: 'rescheduled'
                },
                {
                    event_id: event.id,
                    is_cancelled: false,
                    override_rrule: `DTSTART;TZID=${user.timezone}:${newDt}\nRRULE:FREQ=DAILY;COUNT=1`,
                    duration,
                    room_id: formData.room_id
                        ? Number(formData.room_id)
                        : (event.room_id ?? null),
                    instructor_id: formData.instructor_id
                        ? Number(formData.instructor_id)
                        : (event.instructor_id ?? null)
                }
            ]
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
            toast.error(resp.message || 'Failed to reschedule class');
        }
    }

    const sessionDateLong = event.start.toLocaleDateString('en-US', {
        weekday: 'long',
        month: 'long',
        day: 'numeric'
    });

    return (
        <>
            <FormModal
                open={open}
                onOpenChange={onOpenChange}
                title="Reschedule Class"
                description={`Reschedule the class from ${sessionDateLong}. You can change the date, time, and room.`}
            >
                <Form {...form}>
                    <form
                        onSubmit={(e) => {
                            e.preventDefault();
                            void form.handleSubmit((d) => handleSubmit(d))(e);
                        }}
                    >
                        <div className="space-y-4 py-4">
                            <FormField
                                control={form.control}
                                name="date"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel htmlFor="reschedule-date">
                                            New Date
                                        </FormLabel>
                                        <FormControl>
                                            <Input
                                                id="reschedule-date"
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
                                name="startTime"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel htmlFor="reschedule-start">
                                            New Start Time (optional)
                                        </FormLabel>
                                        <FormControl>
                                            <Input
                                                id="reschedule-start"
                                                type="time"
                                                placeholder={toTimeInput(
                                                    event.start
                                                )}
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <FormField
                                control={form.control}
                                name="endTime"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel htmlFor="reschedule-end">
                                            New End Time (optional)
                                        </FormLabel>
                                        <FormControl>
                                            <Input
                                                id="reschedule-end"
                                                type="time"
                                                placeholder={toTimeInput(
                                                    event.end
                                                )}
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <FormField
                                control={form.control}
                                name="room_id"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel htmlFor="reschedule-room">
                                            New Room (optional)
                                        </FormLabel>
                                        <Select
                                            value={field.value}
                                            onValueChange={field.onChange}
                                        >
                                            <FormControl>
                                                <SelectTrigger id="reschedule-room">
                                                    <SelectValue
                                                        placeholder={
                                                            currentRoomName
                                                                ? `Keep current room (${currentRoomName})`
                                                                : 'Select room'
                                                        }
                                                    />
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
                                        <p className="text-sm text-gray-500">
                                            Leave time and room blank to keep
                                            current values.
                                        </p>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <FormField
                                control={form.control}
                                name="instructor_id"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel htmlFor="reschedule-instructor">
                                            Instructor (optional)
                                        </FormLabel>
                                        <Select
                                            value={field.value}
                                            onValueChange={field.onChange}
                                        >
                                            <FormControl>
                                                <SelectTrigger id="reschedule-instructor">
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

                            <div className="flex gap-2 justify-end">
                                <Button
                                    variant="outline"
                                    type="button"
                                    onClick={() => onOpenChange(false)}
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    disabled={submitting}
                                    className="bg-brand hover:bg-brand-dark text-white"
                                >
                                    {submitting
                                        ? 'Saving...'
                                        : 'Reschedule Class'}
                                </Button>
                            </div>
                        </div>
                    </form>
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
