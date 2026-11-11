import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { toast } from 'sonner';
import useSWR from 'swr';
import { AlertCircle, X } from 'lucide-react';
import API from '@/api/api';
import { canSwitchFacility, useAuth } from '@/auth/useAuth';
import { FormModal } from '@/components/shared';
import { AddInstructorDialog } from '@/components/programs/AddInstructorDialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
import { editClassSchema, EditClassInput } from '@/lib/validation';
import { SelectedClassStatus } from '@/types/attendance';
import { Instructor } from '@/types/events';
import { Cohort, Room, RoomConflict, ServerResponseMany, User } from '@/types';
import {
    conflictSubjectLabel,
    formatRoomConflictRange,
    getInstructorId
} from '@/lib/formatters';

interface EditClassModalProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    cls: Cohort;
    onUpdated: () => void;
}

type EditClassFormData = EditClassInput;

const ALL_DAYS = [
    'Monday',
    'Tuesday',
    'Wednesday',
    'Thursday',
    'Friday',
    'Saturday',
    'Sunday'
];

const DAY_FULL_TO_RRULE: Record<string, string> = {
    Monday: 'MO',
    Tuesday: 'TU',
    Wednesday: 'WE',
    Thursday: 'TH',
    Friday: 'FR',
    Saturday: 'SA',
    Sunday: 'SU'
};

const DAY_RRULE_TO_FULL: Record<string, string> = {
    MO: 'Monday',
    TU: 'Tuesday',
    WE: 'Wednesday',
    TH: 'Thursday',
    FR: 'Friday',
    SA: 'Saturday',
    SU: 'Sunday'
};

function parseScheduleFromEvent(
    recurrenceRule: string,
    duration: string
): {
    days: string[];
    startTime: string;
    endTime: string;
    cadence: string;
    interval: number;
} {
    let startTime = '';
    let endTime = '';
    let days: string[] = [];
    let cadence = 'no-repeat';
    let interval = 1;

    const dtMatch = /T(\d{2})(\d{2})/.exec(recurrenceRule);
    if (dtMatch) {
        startTime = `${dtMatch[1]}:${dtMatch[2]}`;
    }

    const freqMatch = /FREQ=(\w+)/.exec(recurrenceRule);
    if (freqMatch) {
        const freq = freqMatch[1];
        const intervalMatch = /INTERVAL=(\d+)/.exec(recurrenceRule);
        interval = intervalMatch ? Number(intervalMatch[1]) : 1;

        if (freq === 'DAILY') cadence = 'daily';
        else if (freq === 'WEEKLY' && interval === 2) cadence = 'biweekly';
        else if (freq === 'WEEKLY' && interval > 2) cadence = 'custom';
        else if (freq === 'WEEKLY') cadence = 'weekly';
        else if (freq === 'MONTHLY') cadence = 'monthly';
    }

    const byDayMatch = /BYDAY=([A-Z,]+)/.exec(recurrenceRule);
    if (byDayMatch) {
        days = byDayMatch[1]
            .split(',')
            .map((code) => DAY_RRULE_TO_FULL[code] ?? code);
    }

    if (duration && startTime) {
        const durationMatch = /(?:(\d+)h)?(?:(\d+)m)?/.exec(duration);
        if (durationMatch && (durationMatch[1] || durationMatch[2])) {
            const [, hours, mins] = durationMatch;
            const [sh, sm] = startTime.split(':').map(Number);
            const totalMin =
                (sh ?? 0) * 60 +
                (sm ?? 0) +
                Number(hours ?? 0) * 60 +
                Number(mins ?? 0);
            const eh = Math.floor(totalMin / 60) % 24;
            const em = totalMin % 60;
            endTime = `${String(eh).padStart(2, '0')}:${String(em).padStart(2, '0')}`;
        }
    }

    return { days, startTime, endTime, cadence, interval };
}

function buildRecurrenceRule(
    originalRule: string,
    startDate: string,
    startTime: string,
    days: string[],
    cadence: string,
    customInterval?: number,
    endDate?: string
): string {
    const tzMatch = /DTSTART;TZID=([^:]+):/.exec(originalRule);
    const tz = tzMatch?.[1] ?? 'Local';

    const dtStart = `${startDate.replace(/-/g, '')}T${startTime.replace(/:/g, '')}00`;

    if (cadence === 'no-repeat') {
        return `DTSTART;TZID=${tz}:${dtStart}`;
    }

    const freqMap: Record<string, string> = {
        daily: 'DAILY',
        weekly: 'WEEKLY',
        biweekly: 'WEEKLY',
        custom: 'WEEKLY',
        monthly: 'MONTHLY'
    };
    let rule = `DTSTART;TZID=${tz}:${dtStart}\nRRULE:FREQ=${freqMap[cadence] ?? 'WEEKLY'}`;

    if (cadence === 'biweekly') {
        rule += ';INTERVAL=2';
    } else if (cadence === 'custom' && customInterval && customInterval > 1) {
        rule += `;INTERVAL=${customInterval}`;
    }

    if (
        days.length > 0 &&
        (cadence === 'weekly' || cadence === 'biweekly' || cadence === 'custom')
    ) {
        const rruleDays = days.map((d) => DAY_FULL_TO_RRULE[d]).filter(Boolean);
        rule += `;BYDAY=${rruleDays.join(',')}`;
    }

    if (endDate) {
        rule += `;UNTIL=${endDate.replace(/-/g, '')}T235959Z`;
    } else {
        const untilMatch = /UNTIL=([^;\s]+)/.exec(originalRule);
        if (untilMatch) {
            rule += `;UNTIL=${untilMatch[1]}`;
        }
    }

    return rule;
}

function formatDuration(startTime: string, endTime: string): string {
    if (!startTime || !endTime) return '0h0m0s';
    const [sh, sm] = startTime.split(':').map(Number);
    const [eh, em] = endTime.split(':').map(Number);
    const totalMin = (eh ?? 0) * 60 + (em ?? 0) - ((sh ?? 0) * 60 + (sm ?? 0));
    if (totalMin <= 0) return '0h0m0s';
    const hours = Math.floor(totalMin / 60);
    const minutes = totalMin % 60;
    return `${hours}h${minutes}m0s`;
}

export function EditClassModal({
    open,
    onOpenChange,
    cls,
    onUpdated
}: EditClassModalProps) {
    const { user } = useAuth();
    const timezone = user?.timezone ?? 'UTC';

    const activeEvent = cls.events?.find((e) => !e.is_cancelled);
    const initialSchedule = activeEvent
        ? parseScheduleFromEvent(
              activeEvent.recurrence_rule,
              activeEvent.duration
          )
        : {
              days: [],
              startTime: '',
              endTime: '',
              cadence: 'weekly',
              interval: 1
          };

    const [conflicts, setConflicts] = useState<RoomConflict[]>([]);
    const [showAddRoom, setShowAddRoom] = useState(false);
    const [showAddInstructor, setShowAddInstructor] = useState(false);
    // Only department and system admins create admins.
    const canAddInstructor = !!user && canSwitchFacility(user);
    const [newRoomName, setNewRoomName] = useState('');
    const [scheduleDays, setScheduleDays] = useState<string[]>(
        initialSchedule.days
    );
    const [showCustomRecurrence, setShowCustomRecurrence] = useState(false);
    const [customRecurrence, setCustomRecurrence] = useState({
        interval: initialSchedule.interval
    });

    const { data: instructorsResp, mutate: mutateInstructors } = useSWR<
        ServerResponseMany<Instructor>
    >(
        cls.facility_id
            ? `/api/program-facilities/${cls.facility_id}/instructors`
            : null
    );
    const instructors = (instructorsResp?.data ?? []).filter((i) => i.id !== 0);

    const { data: roomsResp, mutate: mutateRooms } = useSWR<
        ServerResponseMany<Room>
    >(
        cls.facility_id
            ? `/api/rooms?facility_id=${cls.facility_id}`
            : '/api/rooms'
    );
    const rooms = roomsResp?.data ?? [];

    const form = useForm<EditClassFormData>({
        resolver: zodResolver(editClassSchema),
        values: {
            class_name: cls.class_name,
            description: cls.description,
            instructor_id: getInstructorId(cls.events) ?? null,
            capacity: cls.capacity,
            credit_hours: cls.credit_hours ?? null,
            start_dt: cls.start_dt.split('T')[0],
            end_dt: cls.end_dt ? cls.end_dt.split('T')[0] : '',
            room_id: activeEvent?.room_id ?? null,
            start_time: initialSchedule.startTime,
            end_time: initialSchedule.endTime,
            cadence: initialSchedule.cadence,
            status: cls.status
        },
        resetOptions: { keepDirtyValues: true }
    });
    const { control, handleSubmit, watch, setValue } = form;
    const isSubmitting = form.formState.isSubmitting;

    const watchedCapacity = watch('capacity');
    const watchedCadence = watch('cadence');
    const watchedStatus = watch('status');
    const watchedStartDt = watch('start_dt');
    const capacityBelowEnrolled =
        watchedCapacity !== undefined && Number(watchedCapacity) < cls.enrolled;

    const toggleDay = (day: string) => {
        setScheduleDays((prev) =>
            prev.includes(day) ? prev.filter((d) => d !== day) : [...prev, day]
        );
    };

    function getCustomRecurrencePreview(): string {
        const { interval } = customRecurrence;
        let preview = `Every ${interval > 1 ? interval + ' ' : ''}`;
        preview += interval === 1 ? 'week' : 'weeks';
        if (scheduleDays.length > 0) {
            preview += ` on ${scheduleDays.join(', ')}`;
        }
        const endDt = watch('end_dt');
        if (endDt) {
            preview += `, ending on ${new Date(endDt).toLocaleDateString()}`;
        } else {
            preview += ', no end date';
        }
        return preview;
    }

    function isCustomRecurrenceValid(): boolean {
        return scheduleDays.length > 0;
    }

    async function handleAddRoom() {
        if (!newRoomName.trim()) return;
        const resp = await API.post<Room, object>(
            `rooms?facility_id=${cls.facility_id}`,
            {
                name: newRoomName.trim()
            }
        );
        if (resp.success) {
            const created = resp.data as unknown as Room;
            await mutateRooms();
            if (created?.id) setValue('room_id', created.id);
            setNewRoomName('');
            setShowAddRoom(false);
            toast.success('Room created');
        } else {
            toast.error('Failed to create room');
        }
    }

    async function handleInstructorCreated(created: User) {
        await mutateInstructors();
        setValue('instructor_id', created.id, {
            shouldDirty: true,
            shouldValidate: true
        });
    }

    async function onSubmit(data: EditClassFormData) {
        if (!activeEvent) return;
        const newRule = buildRecurrenceRule(
            activeEvent.recurrence_rule,
            data.start_dt,
            data.start_time,
            scheduleDays,
            data.cadence,
            data.cadence === 'custom' ? customRecurrence.interval : undefined,
            data.end_dt || undefined // eslint-disable-line @typescript-eslint/prefer-nullish-coalescing -- empty string must collapse to undefined
        );
        const newDuration = formatDuration(data.start_time, data.end_time);
        if (newDuration === '0h0m0s') {
            toast.error('End time must be after start time');
            return;
        }

        const payload = {
            id: cls.id,
            description: data.description,
            instructor_id: data.instructor_id
                ? Number(data.instructor_id)
                : null,
            capacity: Number(data.capacity),
            credit_hours:
                data.credit_hours != null ? Number(data.credit_hours) : null,
            status: data.status,
            start_dt: `${data.start_dt}T00:00:00Z`,
            end_dt: data.end_dt ? `${data.end_dt}T00:00:00Z` : null,
            events: cls.events.map((e) =>
                e.id === activeEvent.id
                    ? {
                          id: e.id,
                          cohort_id: e.cohort_id,
                          duration: newDuration,
                          recurrence_rule: newRule,
                          room_id: data.room_id
                              ? Number(data.room_id)
                              : e.room_id
                      }
                    : {
                          id: e.id,
                          cohort_id: e.cohort_id,
                          duration: e.duration,
                          recurrence_rule: e.recurrence_rule,
                          room_id: e.room_id
                      }
            )
        };

        const resp = await API.patch(
            `programs/${cls.program_id}/classes/${cls.id}`,
            payload
        );

        if (!resp.success) {
            if (resp.status === 409 && Array.isArray(resp.data)) {
                setConflicts(resp.data);
                return;
            }
            toast.error(resp.message || 'Failed to update class');
            return;
        }

        // The name lives on the parent CLASS, not the cohort, so it is a second call to a
        // different tier. Sent AFTER the cohort update on purpose: that call can come back
        // 409 on a room conflict and abort, and a rename that survived a failed save would
        // be a change the admin never confirmed. Only sent when the name actually changed.
        const newName = data.class_name.trim();
        if (newName !== cls.class_name) {
            const renamed = await API.patch<unknown, { name: string }>(
                `classes/${cls.program_class_id}`,
                { name: newName }
            );
            if (!renamed.success) {
                toast.error(
                    renamed.message ||
                        'Class details were saved, but the name could not be updated'
                );
                onUpdated();
                onOpenChange(false);
                return;
            }
        }

        toast.success('Class updated successfully');
        onUpdated();
        onOpenChange(false);
    }

    return (
        <>
            <FormModal
                open={open}
                onOpenChange={onOpenChange}
                title="Edit Class"
                description="Make changes to the class details."
                className="sm:max-w-150 max-h-[90vh] overflow-y-auto"
            >
                <Form {...form}>
                    <form
                        onSubmit={(e) => {
                            e.preventDefault();
                            void handleSubmit(onSubmit)(e);
                        }}
                    >
                        <div className="space-y-6">
                            <div className="space-y-4">
                                <h4 className="font-medium text-brand-dark">
                                    Class Details
                                </h4>

                                <FormField
                                    control={control}
                                    name="class_name"
                                    render={({ field }) => (
                                        <FormItem className="space-y-2">
                                            <FormLabel>Class Name</FormLabel>
                                            <FormControl>
                                                <Input
                                                    id="edit-class-name"
                                                    {...field}
                                                />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />

                                <FormField
                                    control={control}
                                    name="instructor_id"
                                    render={({ field }) => (
                                        <FormItem className="space-y-2">
                                            <FormLabel>Instructor</FormLabel>
                                            <Select
                                                value={
                                                    field.value
                                                        ? String(field.value)
                                                        : undefined
                                                }
                                                onValueChange={(v) => {
                                                    if (v === '__add__') {
                                                        setShowAddInstructor(
                                                            true
                                                        );
                                                        return;
                                                    }
                                                    field.onChange(Number(v));
                                                }}
                                            >
                                                <FormControl>
                                                    <SelectTrigger id="edit-instructor">
                                                        <SelectValue placeholder="Select instructor" />
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent>
                                                    {instructors.map((inst) => (
                                                        <SelectItem
                                                            key={inst.id}
                                                            value={String(
                                                                inst.id
                                                            )}
                                                        >
                                                            {inst.name_last},{' '}
                                                            {inst.name_first}
                                                        </SelectItem>
                                                    ))}
                                                    {canAddInstructor && (
                                                        <SelectItem
                                                            value="__add__"
                                                            className="text-brand font-medium"
                                                        >
                                                            + Add Instructor
                                                        </SelectItem>
                                                    )}
                                                </SelectContent>
                                            </Select>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />

                                <div className="grid grid-cols-2 gap-4">
                                    <FormField
                                        control={control}
                                        name="capacity"
                                        render={({ field }) => (
                                            <FormItem className="space-y-2">
                                                <FormLabel>Capacity</FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="edit-capacity"
                                                        type="number"
                                                        min={1}
                                                        name={field.name}
                                                        ref={field.ref}
                                                        onBlur={field.onBlur}
                                                        value={
                                                            field.value ?? ''
                                                        }
                                                        onChange={(e) =>
                                                            field.onChange(
                                                                e.target
                                                                    .value ===
                                                                    ''
                                                                    ? undefined
                                                                    : e.target
                                                                          .valueAsNumber
                                                            )
                                                        }
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                                {capacityBelowEnrolled && (
                                                    <p className="text-sm text-amber-600 flex items-start gap-1">
                                                        <AlertCircle className="size-4 mt-0.5 flex-shrink-0" />
                                                        <span>
                                                            Warning: Capacity is
                                                            below current
                                                            enrollment (
                                                            {cls.enrolled}{' '}
                                                            students)
                                                        </span>
                                                    </p>
                                                )}
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={control}
                                        name="credit_hours"
                                        render={({ field }) => (
                                            <FormItem className="space-y-2">
                                                <FormLabel>
                                                    Credit Hours
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="edit-credit-hours"
                                                        type="number"
                                                        min={0}
                                                        name={field.name}
                                                        ref={field.ref}
                                                        onBlur={field.onBlur}
                                                        value={
                                                            field.value ?? ''
                                                        }
                                                        onChange={(e) =>
                                                            field.onChange(
                                                                e.target
                                                                    .value ===
                                                                    ''
                                                                    ? null
                                                                    : e.target
                                                                          .valueAsNumber
                                                            )
                                                        }
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                            </div>

                            <FormField
                                control={control}
                                name="description"
                                render={({ field }) => (
                                    <FormItem className="space-y-2">
                                        <FormLabel>Description</FormLabel>
                                        <FormControl>
                                            <textarea
                                                id="edit-description"
                                                rows={3}
                                                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-brand resize-none"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />

                            <div className="space-y-4">
                                <h4 className="font-medium text-brand-dark">
                                    Schedule
                                </h4>

                                <div className="grid grid-cols-3 gap-4">
                                    <FormField
                                        control={control}
                                        name="start_time"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>
                                                    Start Time *
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="edit-start-time"
                                                        type="time"
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />

                                    <FormField
                                        control={control}
                                        name="end_time"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>
                                                    End Time *
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="edit-end-time"
                                                        type="time"
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />

                                    <FormField
                                        control={control}
                                        name="room_id"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>Room *</FormLabel>
                                                <Select
                                                    value={
                                                        field.value
                                                            ? String(
                                                                  field.value
                                                              )
                                                            : ''
                                                    }
                                                    onValueChange={(v) => {
                                                        if (v === '__add__') {
                                                            setShowAddRoom(
                                                                true
                                                            );
                                                            return;
                                                        }
                                                        field.onChange(
                                                            Number(v)
                                                        );
                                                    }}
                                                >
                                                    <FormControl>
                                                        <SelectTrigger id="edit-room">
                                                            <SelectValue placeholder="Select room" />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        {rooms.map((room) => (
                                                            <SelectItem
                                                                key={room.id}
                                                                value={String(
                                                                    room.id
                                                                )}
                                                            >
                                                                {room.name}
                                                            </SelectItem>
                                                        ))}
                                                        <SelectItem
                                                            value="__add__"
                                                            className="text-brand font-medium"
                                                        >
                                                            + Add Room
                                                        </SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                <div className="grid grid-cols-2 gap-4">
                                    <FormField
                                        control={control}
                                        name="start_dt"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>
                                                    Start Date *
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="edit-start-dt"
                                                        type="date"
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                                {cls.status ===
                                                    SelectedClassStatus.Active &&
                                                    watchedStartDt !==
                                                        new Date(cls.start_dt)
                                                            .toISOString()
                                                            .split('T')[0] && (
                                                        <p className="text-sm text-amber-600 flex items-start gap-1">
                                                            <AlertCircle className="size-4 mt-0.5 flex-shrink-0" />
                                                            <span>
                                                                Warning: Cohort
                                                                has already
                                                                started
                                                            </span>
                                                        </p>
                                                    )}
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={control}
                                        name="end_dt"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>End Date</FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="edit-end-dt"
                                                        type="date"
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                <div>
                                    <Label htmlFor="edit-cadence">
                                        Repeats *
                                    </Label>
                                    <FormField
                                        control={control}
                                        name="cadence"
                                        render={({ field }) => (
                                            <Select
                                                value={field.value}
                                                onValueChange={(value) => {
                                                    field.onChange(value);
                                                    if (value === 'custom') {
                                                        setShowCustomRecurrence(
                                                            true
                                                        );
                                                    } else {
                                                        setShowCustomRecurrence(
                                                            false
                                                        );
                                                    }
                                                }}
                                            >
                                                <SelectTrigger id="edit-cadence">
                                                    <SelectValue placeholder="Select recurrence" />
                                                </SelectTrigger>
                                                <SelectContent>
                                                    <SelectItem value="no-repeat">
                                                        Does not repeat
                                                    </SelectItem>
                                                    <SelectItem value="daily">
                                                        Daily
                                                    </SelectItem>
                                                    <SelectItem value="weekly">
                                                        Weekly
                                                    </SelectItem>
                                                    <SelectItem value="biweekly">
                                                        Every 2 weeks
                                                    </SelectItem>
                                                    <SelectItem value="monthly">
                                                        Monthly
                                                    </SelectItem>
                                                    <SelectItem value="custom">
                                                        Custom
                                                    </SelectItem>
                                                </SelectContent>
                                            </Select>
                                        )}
                                    />
                                    {!showCustomRecurrence && (
                                        <p className="text-xs text-gray-500 mt-1">
                                            {watchedCadence === 'weekly' &&
                                                'Class repeats every week on the selected days'}
                                            {watchedCadence === 'biweekly' &&
                                                'Class repeats every 2 weeks on the selected days'}
                                            {watchedCadence === 'daily' &&
                                                'Class repeats every day'}
                                            {watchedCadence === 'monthly' &&
                                                'Class repeats monthly on the same day'}
                                            {watchedCadence === 'no-repeat' &&
                                                'Class is a one-time event'}
                                            {watchedCadence === 'custom' &&
                                                getCustomRecurrencePreview()}
                                        </p>
                                    )}
                                    {watchedCadence === 'custom' &&
                                        !showCustomRecurrence && (
                                            <Button
                                                variant="ghost"
                                                size="sm"
                                                type="button"
                                                onClick={() =>
                                                    setShowCustomRecurrence(
                                                        true
                                                    )
                                                }
                                                className="mt-1 h-7 text-xs text-brand hover:text-brand-dark px-2"
                                            >
                                                Edit pattern
                                            </Button>
                                        )}

                                    {showCustomRecurrence && (
                                        <div className="mt-3 p-4 border border-gray-200 rounded-lg bg-gray-50 space-y-4">
                                            <div className="flex items-center justify-between">
                                                <Label className="text-sm font-medium text-gray-700">
                                                    Custom Recurrence Pattern
                                                </Label>
                                                <Button
                                                    variant="ghost"
                                                    size="sm"
                                                    type="button"
                                                    onClick={() => {
                                                        setShowCustomRecurrence(
                                                            false
                                                        );
                                                        setValue(
                                                            'cadence',
                                                            'weekly'
                                                        );
                                                    }}
                                                    className="h-6 w-6 p-0 -mt-1"
                                                >
                                                    <X className="size-4" />
                                                </Button>
                                            </div>

                                            <div>
                                                <Label
                                                    htmlFor="customInterval"
                                                    className="text-xs"
                                                >
                                                    Repeats every X weeks
                                                </Label>
                                                <Select
                                                    value={customRecurrence.interval.toString()}
                                                    onValueChange={(value) =>
                                                        setCustomRecurrence({
                                                            ...customRecurrence,
                                                            interval:
                                                                parseInt(value)
                                                        })
                                                    }
                                                >
                                                    <SelectTrigger
                                                        id="customInterval"
                                                        className="bg-white"
                                                    >
                                                        <SelectValue />
                                                    </SelectTrigger>
                                                    <SelectContent>
                                                        {[
                                                            1, 2, 3, 4, 5, 6, 7,
                                                            8
                                                        ].map((num) => (
                                                            <SelectItem
                                                                key={num}
                                                                value={num.toString()}
                                                            >
                                                                {num}{' '}
                                                                {num === 1
                                                                    ? 'week'
                                                                    : 'weeks'}
                                                            </SelectItem>
                                                        ))}
                                                    </SelectContent>
                                                </Select>
                                                <p className="text-xs text-gray-500 mt-1">
                                                    Use the &quot;Days of
                                                    Week&quot; selector below to
                                                    choose which days
                                                </p>
                                            </div>

                                            <div className="pt-3 border-t border-gray-200">
                                                <Label className="text-xs text-gray-600">
                                                    Preview
                                                </Label>
                                                <p className="text-sm text-gray-700 mt-1">
                                                    {getCustomRecurrencePreview()}
                                                </p>
                                            </div>

                                            {!isCustomRecurrenceValid() && (
                                                <div className="flex items-center gap-2 text-sm text-destructive">
                                                    <AlertCircle className="size-4" />
                                                    <span>
                                                        Please select at least
                                                        one day of the week
                                                        below
                                                    </span>
                                                </div>
                                            )}

                                            <div className="flex gap-2 pt-2">
                                                <Button
                                                    variant="outline"
                                                    size="sm"
                                                    type="button"
                                                    onClick={() => {
                                                        setShowCustomRecurrence(
                                                            false
                                                        );
                                                        setValue(
                                                            'cadence',
                                                            'weekly'
                                                        );
                                                    }}
                                                    className="flex-1"
                                                >
                                                    Cancel
                                                </Button>
                                                <Button
                                                    size="sm"
                                                    type="button"
                                                    onClick={() => {
                                                        if (
                                                            isCustomRecurrenceValid()
                                                        ) {
                                                            setShowCustomRecurrence(
                                                                false
                                                            );
                                                            toast.success(
                                                                'Custom recurrence pattern applied'
                                                            );
                                                        }
                                                    }}
                                                    disabled={
                                                        !isCustomRecurrenceValid()
                                                    }
                                                    className="flex-1 bg-brand hover:bg-brand-dark"
                                                >
                                                    Apply Pattern
                                                </Button>
                                            </div>
                                        </div>
                                    )}

                                    {(watchedCadence === 'weekly' ||
                                        watchedCadence === 'biweekly' ||
                                        watchedCadence === 'custom') && (
                                        <div className="mt-2">
                                            <Label>Days of Week *</Label>
                                            <div className="flex flex-wrap gap-2 mt-2">
                                                {ALL_DAYS.map((day) => {
                                                    const isSelected =
                                                        scheduleDays.includes(
                                                            day
                                                        );
                                                    return (
                                                        <button
                                                            key={day}
                                                            type="button"
                                                            onClick={() =>
                                                                toggleDay(day)
                                                            }
                                                            className={`px-4 py-2 rounded-lg border transition-colors ${
                                                                isSelected
                                                                    ? 'bg-brand text-white border-brand'
                                                                    : 'bg-white text-gray-700 border-gray-300 hover:border-brand'
                                                            }`}
                                                        >
                                                            {day.slice(0, 3)}
                                                        </button>
                                                    );
                                                })}
                                            </div>
                                        </div>
                                    )}
                                </div>
                            </div>

                            <div className="space-y-4">
                                <h4 className="font-medium text-brand-dark">
                                    Status
                                </h4>
                                <div className="space-y-2">
                                    <FormField
                                        control={control}
                                        name="status"
                                        render={({ field }) => (
                                            <Select
                                                value={field.value}
                                                onValueChange={field.onChange}
                                            >
                                                <SelectTrigger>
                                                    <SelectValue />
                                                </SelectTrigger>
                                                <SelectContent>
                                                    <SelectItem
                                                        value={
                                                            SelectedClassStatus.Scheduled
                                                        }
                                                    >
                                                        Scheduled
                                                    </SelectItem>
                                                    <SelectItem
                                                        value={
                                                            SelectedClassStatus.Active
                                                        }
                                                    >
                                                        Active
                                                    </SelectItem>
                                                    <SelectItem
                                                        value={
                                                            SelectedClassStatus.Paused
                                                        }
                                                    >
                                                        Paused
                                                    </SelectItem>
                                                    <SelectItem
                                                        value={
                                                            SelectedClassStatus.Completed
                                                        }
                                                    >
                                                        Completed
                                                    </SelectItem>
                                                    <SelectItem
                                                        value={
                                                            SelectedClassStatus.Cancelled
                                                        }
                                                    >
                                                        Cancelled
                                                    </SelectItem>
                                                </SelectContent>
                                            </Select>
                                        )}
                                    />
                                    {watchedStatus === 'Paused' && (
                                        <p className="text-sm text-gray-600 flex items-start gap-1">
                                            <AlertCircle className="size-4 mt-0.5 flex-shrink-0" />
                                            <span>
                                                Pausing will hide this class
                                                from daily attendance views
                                            </span>
                                        </p>
                                    )}
                                    {watchedStatus === 'Cancelled' && (
                                        <p className="text-sm text-gray-600 flex items-start gap-1">
                                            <AlertCircle className="size-4 mt-0.5 flex-shrink-0" />
                                            <span>
                                                Cancelling will end this class
                                                and update all enrollments
                                            </span>
                                        </p>
                                    )}
                                </div>
                            </div>

                            {conflicts.length > 0 && (
                                <div className="space-y-2">
                                    <p className="text-sm font-medium text-red-700">
                                        {conflictSubjectLabel(conflicts)} has{' '}
                                        {conflicts.length} scheduling{' '}
                                        {conflicts.length === 1
                                            ? 'conflict'
                                            : 'conflicts'}
                                        :
                                    </p>
                                    <div className="max-h-32 overflow-y-auto space-y-2">
                                        {conflicts.map((c, i) => (
                                            <div
                                                key={i}
                                                className="bg-red-50 p-2 rounded text-sm"
                                            >
                                                <p className="font-medium text-red-800">
                                                    {c.class_name}
                                                </p>
                                                <p className="text-red-600">
                                                    {formatRoomConflictRange(
                                                        c.start_time,
                                                        c.end_time,
                                                        timezone
                                                    )}
                                                </p>
                                            </div>
                                        ))}
                                    </div>
                                </div>
                            )}
                        </div>

                        <div className="mt-6 flex justify-end gap-2">
                            <Button
                                type="button"
                                variant="outline"
                                onClick={() => onOpenChange(false)}
                            >
                                Cancel
                            </Button>
                            <Button
                                type="submit"
                                disabled={isSubmitting}
                                className="bg-brand hover:bg-brand-dark text-white"
                            >
                                {isSubmitting ? 'Saving...' : 'Save Changes'}
                            </Button>
                        </div>
                    </form>
                </Form>
            </FormModal>

            <FormModal
                open={showAddRoom}
                onOpenChange={setShowAddRoom}
                title="Add Room"
            >
                <div className="space-y-4">
                    <div className="space-y-2">
                        <Label htmlFor="edit-new-room-name">Room Name</Label>
                        <Input
                            id="edit-new-room-name"
                            value={newRoomName}
                            onChange={(e) => setNewRoomName(e.target.value)}
                            placeholder="e.g. Room 101"
                        />
                    </div>
                    <div className="flex justify-end gap-2">
                        <Button
                            variant="outline"
                            onClick={() => setShowAddRoom(false)}
                        >
                            Cancel
                        </Button>
                        <Button
                            className="btn-brand-dark"
                            onClick={() => void handleAddRoom()}
                        >
                            Create
                        </Button>
                    </div>
                </div>
            </FormModal>

            {canAddInstructor && !!cls.facility_id && (
                <AddInstructorDialog
                    open={showAddInstructor}
                    onOpenChange={setShowAddInstructor}
                    facilityId={cls.facility_id}
                    onCreated={(created) =>
                        void handleInstructorCreated(created)
                    }
                />
            )}
        </>
    );
}
