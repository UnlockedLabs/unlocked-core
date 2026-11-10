import { useState, useEffect } from 'react';
import { useNavigate, useParams, useLoaderData } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { toast } from 'sonner';
import useSWR from 'swr';
import type { Class as ProgramClassSummary } from '@/types/program';
import API from '@/api/api';
import {
    ANALYTICS_EVENTS,
    captureEvent,
    flowTimerSeconds,
    programFacilityProps
} from '@/lib/events';
import { useFlowTimer } from '@/lib/useFlowTimer';
import { useAuth } from '@/auth/useAuth';
import {
    ClassLoaderData,
    Instructor,
    ProgClassStatus,
    ServerResponseMany,
    Room,
    RoomConflict,
    User
} from '@/types';
import {
    buildClassManagementSchema,
    ClassManagementInput,
    ClassManagementFormValues
} from '@/lib/validation';
import { isCompletedCancelledOrArchived } from '@/lib/classStatus';
import {
    conflictSubjectLabel,
    formatRoomConflictRange,
    getInstructorId
} from '@/lib/formatters';
import { PageHeader } from '@/components/shared';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
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
import { FormModal } from '@/components/shared';
import { AlertCircle, X } from 'lucide-react';

type ClassFormData = ClassManagementInput;
type ClassFormValues = ClassManagementFormValues;

interface ClassManagementFormProps {
    programId: string;
    classId?: string;
    loaderData?: ClassLoaderData;
    onCancel?: () => void;
    onCreated?: () => void;
    embedded?: boolean;
    facilityId?: number;
}

const WEEKDAY_OPTIONS = [
    { label: 'Monday', value: 'MO' },
    { label: 'Tuesday', value: 'TU' },
    { label: 'Wednesday', value: 'WE' },
    { label: 'Thursday', value: 'TH' },
    { label: 'Friday', value: 'FR' },
    { label: 'Saturday', value: 'SA' },
    { label: 'Sunday', value: 'SU' }
];

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

function buildRRule(
    startDt: string,
    startTime: string,
    days: string[],
    endDt?: string,
    cadence: ClassFormData['cadence'] = 'weekly',
    customInterval?: number,
    timezone?: string
): string {
    const dtStart = `${startDt.replace(/-/g, '')}T${startTime.replace(/:/g, '')}00`;
    const tz = timezone ?? 'Local';
    let rule = `DTSTART;TZID=${tz}:${dtStart}\nRRULE:`;
    if (cadence === 'no-repeat') {
        return `${rule}FREQ=DAILY;COUNT=1`;
    }
    if (cadence === 'daily') {
        rule += 'FREQ=DAILY';
    } else if (cadence === 'custom') {
        const interval = Math.max(1, customInterval ?? 1);
        rule += `FREQ=WEEKLY;INTERVAL=${interval}`;
        if (days.length > 0) {
            rule += `;BYDAY=${days.join(',')}`;
        }
    } else if (cadence === 'monthly') {
        rule += `FREQ=MONTHLY;BYMONTHDAY=${startDt.split('-')[2]}`;
    } else {
        rule += 'FREQ=WEEKLY;INTERVAL=1';
        if (cadence === 'biweekly') {
            rule += ';INTERVAL=2';
        }
        if (days.length > 0) {
            rule += `;BYDAY=${days.join(',')}`;
        }
    }
    if (endDt) {
        rule += `;UNTIL=${endDt.replace(/-/g, '')}T235959Z`;
    }
    return rule;
}

export function ClassManagementFormInner({
    programId,
    classId,
    loaderData,
    onCancel,
    onCreated,
    embedded = false,
    facilityId: facilityIdProp
}: ClassManagementFormProps) {
    const { user } = useAuth();
    const timezone = user?.timezone ?? 'UTC';
    const navigate = useNavigate();

    const isNewClass = classId === 'new' || !classId;
    const existingClass = loaderData?.class;
    const blockEdits = existingClass
        ? isCompletedCancelledOrArchived(existingClass)
        : false;

    const NEW_CLASS = '__other__';
    const [selectedClassId, setSelectedClassId] = useState<string>('');
    const [newClassName, setNewClassName] = useState('');

    // Declared before the fetch below, which scopes on it.
    const resolvedFacilityId = facilityIdProp ?? user?.facility.id;

    /*
     * MUST be scoped by facility, not just program. A class belongs to exactly one
     * facility, and program_class_cohorts_class_parent_fkey is composite on
     * (class_id, program_id, facility_id) -- so a class from another facility is never
     * a legal parent. Unscoped, a statewide admin sees the same class name once per
     * facility (13x for "Adult Basic Education"), and picking the wrong one fails the
     * FK at insert time rather than being rejected in the form.
     */
    const { data: classListResp, mutate: mutateClassList } = useSWR<
        ServerResponseMany<ProgramClassSummary>,
        Error
    >(
        isNewClass && programId
            ? `/api/classes?program_id=${programId}&per_page=100${
                  resolvedFacilityId ? `&facility_id=${resolvedFacilityId}` : ''
              }`
            : null
    );
    const existingClasses = classListResp?.data ?? [];

    const [rooms, setRooms] = useState<Room[]>(loaderData?.rooms ?? []);
    const [conflicts, setConflicts] = useState<RoomConflict[]>([]);
    const [showConflicts, setShowConflicts] = useState(false);
    const [showAddRoom, setShowAddRoom] = useState(false);
    const [showAddInstructor, setShowAddInstructor] = useState(false);
    const [newRoomName, setNewRoomName] = useState('');
    const [newInstructor, setNewInstructor] = useState({
        name_first: '',
        name_last: '',
        username: ''
    });
    const [roomSelectValue, setRoomSelectValue] = useState('');
    const [pendingRoomId, setPendingRoomId] = useState<number | null>(null);
    const [showCustomRecurrence, setShowCustomRecurrence] = useState(false);
    const [customRecurrenceInterval, setCustomRecurrenceInterval] = useState(1);

    const [instructors, setInstructors] = useState<Instructor[]>([]);
    const startMsRef = useFlowTimer(
        isNewClass ? ANALYTICS_EVENTS.ClassCreationStarted : null,
        programFacilityProps(resolvedFacilityId)
    );

    useEffect(() => {
        if (!resolvedFacilityId) return;
        async function fetchInstructors() {
            const resp = await API.get<Instructor>(
                `program-facilities/${resolvedFacilityId}/instructors`
            );
            if (resp.success && resp.data) {
                setInstructors(
                    (resp.data as Instructor[]).filter((i) => i.id !== 0)
                );
            }
        }
        void fetchInstructors();
    }, [resolvedFacilityId]);

    const { data: roomsResp } = useSWR<ServerResponseMany<Room>>(
        rooms.length === 0
            ? resolvedFacilityId
                ? `/api/rooms?facility_id=${resolvedFacilityId}`
                : '/api/rooms'
            : null
    );

    const form = useForm<ClassFormValues, unknown, ClassFormData>({
        resolver: zodResolver(buildClassManagementSchema(isNewClass)),
        defaultValues: {
            description: '',
            instructor_id: null,
            capacity: 20,
            credit_hours: 0,
            start_dt: '',
            end_dt: '',
            status: ProgClassStatus.SCHEDULED,
            room_id: null,
            start_time: '',
            end_time: '',
            days: [],
            cadence: 'weekly'
        }
    });
    const {
        control,
        handleSubmit,
        reset,
        setValue,
        watch,
        formState: { isSubmitting }
    } = form;

    useEffect(() => {
        if (roomsResp?.data && rooms.length === 0) {
            setRooms(roomsResp.data);
        }
    }, [roomsResp, rooms.length]);

    const watchedDays = watch('days') ?? [];
    const watchedCadence = watch('cadence');
    const watchedEndDate = watch('end_dt');
    const watchedRoomId = watch('room_id');

    useEffect(() => {
        if (!pendingRoomId) return;
        setValue('room_id', pendingRoomId, {
            shouldDirty: true,
            shouldValidate: true
        });
        setPendingRoomId(null);
    }, [pendingRoomId, setValue]);

    useEffect(() => {
        if (!watchedRoomId) {
            setRoomSelectValue('');
            return;
        }
        setRoomSelectValue(String(watchedRoomId));
    }, [watchedRoomId]);

    useEffect(() => {
        if (existingClass && !isNewClass) {
            const event = existingClass.events?.[0];
            let startTime = '';
            let endTime = '';
            let selectedDays: string[] = [];

            if (event?.recurrence_rule) {
                try {
                    const dtMatch = /T(\d{2})(\d{2})/.exec(
                        event.recurrence_rule
                    );
                    if (dtMatch) {
                        startTime = `${dtMatch[1]}:${dtMatch[2]}`;
                    }
                    const byDayMatch = /BYDAY=([A-Z,]+)/.exec(
                        event.recurrence_rule
                    );
                    if (byDayMatch) {
                        selectedDays = byDayMatch[1].split(',');
                    }
                } catch {
                    /* parse failure */
                }
            }

            if (event?.duration && startTime) {
                const durationMatch = /(\d+)h(\d+)m/.exec(event.duration);
                if (durationMatch) {
                    const [, hours, mins] = durationMatch;
                    const [sh, sm] = startTime.split(':').map(Number);
                    const totalMin =
                        (sh ?? 0) * 60 +
                        (sm ?? 0) +
                        Number(hours) * 60 +
                        Number(mins);
                    const eh = Math.floor(totalMin / 60) % 24;
                    const em = totalMin % 60;
                    endTime = `${String(eh).padStart(2, '0')}:${String(em).padStart(2, '0')}`;
                }
            }

            reset({
                description: existingClass.description,
                instructor_id: getInstructorId(existingClass.events) ?? null,
                capacity: existingClass.capacity,
                credit_hours: existingClass.credit_hours,
                start_dt: new Date(existingClass.start_dt)
                    .toISOString()
                    .split('T')[0],
                end_dt: existingClass.end_dt
                    ? new Date(existingClass.end_dt).toISOString().split('T')[0]
                    : '',
                status: existingClass.status,
                room_id: event?.room_id ?? null,
                start_time: startTime,
                end_time: endTime,
                days: selectedDays,
                cadence: 'weekly'
            });
        }
    }, [existingClass, isNewClass, reset]);

    const focusRing =
        'focus-visible:border-[#b3b3b3] focus-visible:ring-[#b3b3b3]/50';
    const focusRingButton =
        'focus-visible:ring-[#b3b3b3]/50 focus-visible:ring-[1px] focus-visible:outline-hidden';

    function getSelectedDayLabels(days: string[]) {
        return WEEKDAY_OPTIONS.filter((opt) => days.includes(opt.value)).map(
            (opt) => opt.label
        );
    }

    function getCustomRecurrencePreview() {
        const interval = Math.max(1, customRecurrenceInterval);
        const dayLabels = getSelectedDayLabels(watchedDays);
        let preview = `Every ${interval > 1 ? `${interval} ` : ''}${
            interval === 1 ? 'week' : 'weeks'
        }`;
        if (dayLabels.length > 0) {
            preview += ` on ${dayLabels.join(', ')}`;
        }
        if (watchedEndDate) {
            preview += `, ending on ${new Date(
                watchedEndDate
            ).toLocaleDateString()}`;
        } else {
            preview += ', no end date';
        }
        return preview;
    }

    function isCustomRecurrenceValid() {
        return watchedDays.length > 0;
    }

    function cadenceHelpText(value: ClassFormData['cadence']) {
        switch (value) {
            case 'weekly':
                return 'Class repeats every week on the selected days';
            case 'biweekly':
                return 'Class repeats every 2 weeks on the selected days';
            case 'daily':
                return 'Class repeats every day';
            case 'monthly':
                return 'Class repeats monthly on the same day';
            case 'no-repeat':
                return 'Class is a one-time event';
            case 'custom':
                return isCustomRecurrenceValid()
                    ? getCustomRecurrencePreview()
                    : '';
            default:
                return '';
        }
    }

    function toggleDay(day: string) {
        const current = watchedDays;
        const updated = current.includes(day)
            ? current.filter((d) => d !== day)
            : [...current, day];
        setValue('days', updated, {
            shouldDirty: true,
            shouldValidate: true
        });
    }

    async function handleAddRoom() {
        if (!newRoomName.trim()) return;
        const roomUrl = resolvedFacilityId
            ? `rooms?facility_id=${resolvedFacilityId}`
            : 'rooms';
        const resp = await API.post<Room, object>(roomUrl, {
            name: newRoomName.trim()
        });
        if (resp.success) {
            const created = resp.data as unknown as Room;
            setRooms((prev) => [...prev, created]);
            setPendingRoomId(created.id);
            setRoomSelectValue(String(created.id));
            toast.success('Room created');
        } else {
            toast.error('Failed to create room');
        }
        setNewRoomName('');
        setShowAddRoom(false);
    }

    async function handleAddInstructor() {
        const { name_first, name_last, username } = newInstructor;
        if (!name_first.trim() || !name_last.trim() || !username.trim()) return;
        const resp = await API.post<
            { user: User; temp_password: string },
            object
        >('users', {
            user: {
                name_first: name_first.trim(),
                name_last: name_last.trim(),
                username: username.trim(),
                role: 'facility_admin',
                // An admin who can switch facilities must name one, or the
                // server answers "facility selection is required".
                facility_id: resolvedFacilityId
            },
            provider_platforms: []
        });
        if (resp.success) {
            const newUser = (resp.data as { user: User; temp_password: string })
                .user;
            setInstructors((prev) => [
                ...prev,
                newUser as unknown as Instructor
            ]);
            toast.success('Instructor created');
        } else {
            toast.error(resp.message || 'Failed to create instructor');
        }
        setNewInstructor({ name_first: '', name_last: '', username: '' });
        setShowAddInstructor(false);
    }

    async function onSubmit(data: ClassFormData) {
        if (blockEdits) {
            toast.error('Cannot update classes that are complete or cancelled');
            return;
        }

        const duration = formatDuration(data.start_time, data.end_time);
        const rrule = isNewClass
            ? buildRRule(
                  data.start_dt,
                  data.start_time,
                  data.days,
                  data.end_dt,
                  data.cadence,
                  customRecurrenceInterval,
                  user?.timezone
              )
            : (existingClass?.events?.[0]?.recurrence_rule ?? '');

        const payload = {
            ...(classId && classId !== 'new' && { id: Number(classId) }),
            description: data.description,
            instructor_id: data.instructor_id
                ? Number(data.instructor_id)
                : null,
            capacity: Number(data.capacity),
            credit_hours:
                Number(data.credit_hours) > 0
                    ? Number(data.credit_hours)
                    : null,
            start_dt: new Date(data.start_dt),
            end_dt: data.end_dt ? new Date(data.end_dt) : null,
            status: data.status,
            events: isNewClass
                ? [
                      {
                          recurrence_rule: rrule,
                          room_id: data.room_id ? Number(data.room_id) : null,
                          duration
                      }
                  ]
                : [
                      {
                          id: existingClass!.events[0].id,
                          cohort_id: existingClass!.events[0].cohort_id,
                          duration: existingClass!.events[0].duration,
                          recurrence_rule:
                              existingClass!.events[0].recurrence_rule,
                          room_id: data.room_id
                              ? Number(data.room_id)
                              : existingClass!.events[0].room_id
                      },
                      ...existingClass!.events.slice(1)
                  ]
        };

        // Resolve the parent class before creating the cohort. Picking "Other" creates
        // the class first so the cohort attaches to a real id -- rather than relying on
        // the backend's auto-parent hook, which would name the class after the COHORT.
        let parentClassId: number | null = null;
        if (isNewClass) {
            if (selectedClassId === NEW_CLASS) {
                const trimmed = newClassName.trim();
                if (!trimmed) {
                    toast.error('Enter a name for the new class');
                    return;
                }
                // facility_id must ride on the query string, and must be the SAME
                // facility the cohort below is created with. The backend reads it via
                // requireFacilityID -> getQueryContext, which yields 0 (not the
                // caller's own facility) for an admin who can switch facilities --
                // so omitting it 400s with "facility selection is required", and
                // sending a different one builds a class the cohort's composite FK
                // will reject.
                const created = await API.post<{ id: number }, unknown>(
                    resolvedFacilityId
                        ? `classes?facility_id=${resolvedFacilityId}`
                        : 'classes',
                    {
                        program_id: Number(programId),
                        name: trimmed,
                        description: data.description,
                        credit_hours:
                            Number(data.credit_hours) > 0
                                ? Number(data.credit_hours)
                                : null
                    }
                );
                if (!created.success) {
                    toast.error(
                        created.message || 'Failed to create the class'
                    );
                    return;
                }
                parentClassId = (created.data as { id: number }).id;
                // Pin the form to the class we just created. The cohort POST below can
                // still fail -- a 409 room/instructor conflict is ordinary flow -- and
                // that request is NOT in the same transaction as this one, so the class
                // already exists. Without this, resubmitting after fixing the conflict
                // re-enters this branch and creates a SECOND class with the same name,
                // orphaning the first and showing a duplicate in the dropdown.
                setSelectedClassId(String(parentClassId));
                setNewClassName('');
                await mutateClassList();
            } else if (selectedClassId) {
                parentClassId = Number(selectedClassId);
            } else {
                toast.error('Choose a class');
                return;
            }
            (payload as Record<string, unknown>).program_class_id =
                parentClassId;
        }

        const createUrl = resolvedFacilityId
            ? `programs/${programId}/classes?facility_id=${resolvedFacilityId}`
            : `programs/${programId}/classes`;
        const resp = isNewClass
            ? await API.post(createUrl, payload)
            : await API.patch(
                  `programs/${programId}/classes/${classId}`,
                  payload
              );

        if (!resp.success) {
            if (resp.status === 409 && Array.isArray(resp.data)) {
                setConflicts(resp.data as unknown as RoomConflict[]);
                setShowConflicts(true);
                return;
            }
            toast.error(
                resp.message ||
                    (isNewClass
                        ? 'Failed to create class'
                        : 'Failed to update class')
            );
            return;
        }

        toast.success(
            isNewClass
                ? 'Class created successfully'
                : 'Class updated successfully'
        );
        if (isNewClass) {
            captureEvent(ANALYTICS_EVENTS.ClassCreationCompleted, {
                duration_seconds: flowTimerSeconds(startMsRef.current),
                ...programFacilityProps(resolvedFacilityId)
            });
            if (onCreated) {
                onCreated();
            }
            if (!embedded) {
                navigate(`/programs/${programId}`);
            }
        } else {
            if (onCreated) {
                onCreated();
            }
            if (!embedded) {
                navigate(`/program-classes/${classId}/detail`);
            }
        }
    }

    const statusOptions = isNewClass
        ? {
              Scheduled: ProgClassStatus.SCHEDULED,
              Active: ProgClassStatus.ACTIVE
          }
        : ProgClassStatus;

    // The parent-class selector is shared by BOTH create layouts -- the embedded form on
    // the program page AND the standalone route programs/:id/classes/:class_id?. It has to
    // be: onSubmit refuses to create a cohort without a resolved parent ("Choose a class"),
    // so a create layout that cannot set selectedClassId cannot create anything at all.
    const parentClassSelector = (
        <div className="mb-4 space-y-3">
            <div>
                <label htmlFor="parent-class" className="text-sm font-medium">
                    Class *
                </label>
                <select
                    id="parent-class"
                    className={`mt-1 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ${focusRing}`}
                    value={selectedClassId}
                    onChange={(e) => {
                        const value = e.target.value;
                        setSelectedClassId(value);
                    }}
                >
                    <option value="">Select a class…</option>
                    {existingClasses
                        .filter((c) => !c.archived_at)
                        .map((c) => (
                            <option key={c.id} value={String(c.id)}>
                                {c.name}
                            </option>
                        ))}
                    <option value={NEW_CLASS}>Other — add a new class</option>
                </select>
            </div>

            {selectedClassId === NEW_CLASS && (
                <div>
                    <label
                        htmlFor="new-class-name"
                        className="text-sm font-medium"
                    >
                        New class name *
                    </label>
                    <Input
                        id="new-class-name"
                        className={`mt-1 ${focusRing}`}
                        placeholder="e.g., Anger Management"
                        value={newClassName}
                        onChange={(e) => setNewClassName(e.target.value)}
                    />
                    <p className="text-xs text-muted-foreground mt-1">
                        Name the class itself, not the schedule — leave times
                        like &quot;(9am-10am)&quot; out of it.
                    </p>
                </div>
            )}
        </div>
    );

    return (
        <div className="space-y-6">
            {!embedded && (
                <PageHeader
                    title={isNewClass ? 'Create Class' : 'Edit Class'}
                    subtitle={
                        isNewClass
                            ? 'Set up a new class for this program'
                            : 'Update class details'
                    }
                />
            )}

            <Form {...form}>
                <form
                    onSubmit={(event) => void handleSubmit(onSubmit)(event)}
                    className="space-y-6"
                >
                    {embedded && (
                        <div className="space-y-6">
                            <div>
                                <h4 className="text-sm text-gray-700 mb-3">
                                    Basic Information
                                </h4>
                                {isNewClass && parentClassSelector}

                                <div className="grid grid-cols-2 items-start gap-4">
                                    <FormField
                                        control={control}
                                        name="instructor_id"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>
                                                    Instructor *
                                                </FormLabel>
                                                <Select
                                                    value={
                                                        field.value
                                                            ? String(
                                                                  field.value
                                                              )
                                                            : ''
                                                    }
                                                    onValueChange={(v) =>
                                                        field.onChange(
                                                            Number(v)
                                                        )
                                                    }
                                                >
                                                    <FormControl>
                                                        <SelectTrigger
                                                            className={
                                                                focusRing
                                                            }
                                                        >
                                                            <SelectValue placeholder="Select instructor" />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        {instructors.map(
                                                            (inst) => (
                                                                <SelectItem
                                                                    key={
                                                                        inst.id
                                                                    }
                                                                    value={String(
                                                                        inst.id
                                                                    )}
                                                                >
                                                                    {
                                                                        inst.name_last
                                                                    }
                                                                    ,{' '}
                                                                    {
                                                                        inst.name_first
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
                                        control={control}
                                        name="status"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>
                                                    Initial Status *
                                                </FormLabel>
                                                <Select
                                                    value={field.value}
                                                    onValueChange={
                                                        field.onChange
                                                    }
                                                >
                                                    <FormControl>
                                                        <SelectTrigger
                                                            id="status"
                                                            className={
                                                                focusRing
                                                            }
                                                        >
                                                            <SelectValue placeholder="Select status" />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        <SelectItem
                                                            value={
                                                                ProgClassStatus.SCHEDULED
                                                            }
                                                        >
                                                            Scheduled
                                                        </SelectItem>
                                                        <SelectItem
                                                            value={
                                                                ProgClassStatus.ACTIVE
                                                            }
                                                        >
                                                            Active
                                                        </SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <p className="text-xs text-gray-500 mt-1">
                                                    Scheduled classes will start
                                                    on the start date. Active
                                                    classes are already running.
                                                </p>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={control}
                                        name="capacity"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>
                                                    Capacity *
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        type="number"
                                                        min={1}
                                                        className={focusRing}
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
                                            </FormItem>
                                        )}
                                    />
                                    <FormField
                                        control={control}
                                        name="description"
                                        render={({ field }) => (
                                            <FormItem className="col-span-2">
                                                <FormLabel>
                                                    Description *
                                                </FormLabel>
                                                <FormControl>
                                                    <Textarea
                                                        placeholder="Brief description of the class"
                                                        rows={2}
                                                        className={focusRing}
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                            </div>

                            <div className="pt-4 border-t border-gray-200">
                                <h4 className="text-sm text-gray-700 mb-3">
                                    Schedule
                                </h4>
                                <div className="space-y-4">
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
                                                            type="time"
                                                            className={
                                                                focusRing
                                                            }
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
                                                            type="time"
                                                            className={
                                                                focusRing
                                                            }
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
                                                    <FormLabel>
                                                        Room *
                                                    </FormLabel>
                                                    <Select
                                                        value={roomSelectValue}
                                                        onValueChange={(v) => {
                                                            if (
                                                                v === '__add__'
                                                            ) {
                                                                setShowAddRoom(
                                                                    true
                                                                );
                                                                return;
                                                            }
                                                            setRoomSelectValue(
                                                                v
                                                            );
                                                            field.onChange(
                                                                Number(v)
                                                            );
                                                        }}
                                                    >
                                                        <FormControl>
                                                            <SelectTrigger
                                                                id="room"
                                                                className={
                                                                    focusRing
                                                                }
                                                            >
                                                                <SelectValue placeholder="Select room" />
                                                            </SelectTrigger>
                                                        </FormControl>
                                                        <SelectContent>
                                                            {rooms.map(
                                                                (room) => (
                                                                    <SelectItem
                                                                        key={
                                                                            room.id
                                                                        }
                                                                        value={String(
                                                                            room.id
                                                                        )}
                                                                    >
                                                                        {
                                                                            room.name
                                                                        }
                                                                    </SelectItem>
                                                                )
                                                            )}
                                                            <SelectItem value="__add__">
                                                                Add Custom Room
                                                            </SelectItem>
                                                        </SelectContent>
                                                    </Select>
                                                    <FormMessage />
                                                    {showAddRoom && (
                                                        <div className="mt-3 p-3 border border-gray-200 rounded-md bg-gray-50 space-y-3 animate-in slide-in-from-top-2 duration-200">
                                                            <div className="flex items-start justify-between">
                                                                <div className="flex-1">
                                                                    <Label
                                                                        htmlFor="new-room-name"
                                                                        className="text-sm font-medium"
                                                                    >
                                                                        Custom
                                                                        Room
                                                                        Name
                                                                    </Label>
                                                                    <p className="text-xs text-gray-500 mt-1">
                                                                        Room
                                                                        names
                                                                        should
                                                                        be
                                                                        specific
                                                                        (e.g.,
                                                                        "Library
                                                                        - Room
                                                                        3A")
                                                                    </p>
                                                                </div>
                                                                <Button
                                                                    variant="ghost"
                                                                    size="sm"
                                                                    onClick={() => {
                                                                        setShowAddRoom(
                                                                            false
                                                                        );
                                                                        setNewRoomName(
                                                                            ''
                                                                        );
                                                                    }}
                                                                    className="h-6 w-6 p-0 -mt-1"
                                                                    type="button"
                                                                >
                                                                    <X className="size-4" />
                                                                </Button>
                                                            </div>
                                                            <Input
                                                                id="new-room-name"
                                                                placeholder="e.g., Education Wing - Room 205"
                                                                value={
                                                                    newRoomName
                                                                }
                                                                onChange={(e) =>
                                                                    setNewRoomName(
                                                                        e.target
                                                                            .value
                                                                    )
                                                                }
                                                                className={
                                                                    focusRing
                                                                }
                                                                autoFocus
                                                            />
                                                            {newRoomName &&
                                                                rooms.some(
                                                                    (room) =>
                                                                        room.name.toLowerCase() ===
                                                                        newRoomName
                                                                            .trim()
                                                                            .toLowerCase()
                                                                ) && (
                                                                    <div className="flex items-center gap-2 text-sm text-orange-600">
                                                                        <AlertCircle className="size-4" />
                                                                        <span>
                                                                            This
                                                                            room
                                                                            name
                                                                            already
                                                                            exists
                                                                            in
                                                                            the
                                                                            list
                                                                            above
                                                                        </span>
                                                                    </div>
                                                                )}
                                                            <div className="flex gap-2">
                                                                <Button
                                                                    variant="outline"
                                                                    size="sm"
                                                                    onClick={() => {
                                                                        setShowAddRoom(
                                                                            false
                                                                        );
                                                                        setNewRoomName(
                                                                            ''
                                                                        );
                                                                    }}
                                                                    className="flex-1"
                                                                    type="button"
                                                                >
                                                                    Cancel
                                                                </Button>
                                                                <Button
                                                                    size="sm"
                                                                    onClick={() =>
                                                                        void handleAddRoom()
                                                                    }
                                                                    disabled={
                                                                        !newRoomName.trim()
                                                                    }
                                                                    className="flex-1 bg-brand hover:bg-brand-dark"
                                                                    type="button"
                                                                >
                                                                    Add Room
                                                                </Button>
                                                            </div>
                                                        </div>
                                                    )}
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
                                                            type="date"
                                                            className={
                                                                focusRing
                                                            }
                                                            {...field}
                                                        />
                                                    </FormControl>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />
                                        <FormField
                                            control={control}
                                            name="end_dt"
                                            render={({ field }) => (
                                                <FormItem>
                                                    <FormLabel>
                                                        End Date
                                                    </FormLabel>
                                                    <FormControl>
                                                        <Input
                                                            type="date"
                                                            className={
                                                                focusRing
                                                            }
                                                            {...field}
                                                        />
                                                    </FormControl>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />
                                    </div>
                                    <FormField
                                        control={control}
                                        name="cadence"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>Repeats *</FormLabel>
                                                <Select
                                                    value={field.value}
                                                    onValueChange={(value) => {
                                                        field.onChange(value);
                                                        if (
                                                            value === 'custom'
                                                        ) {
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
                                                    <FormControl>
                                                        <SelectTrigger
                                                            id="cadence"
                                                            className={
                                                                focusRing
                                                            }
                                                        >
                                                            <SelectValue placeholder="Select recurrence" />
                                                        </SelectTrigger>
                                                    </FormControl>
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
                                                <p className="text-xs text-gray-500 mt-1">
                                                    {cadenceHelpText(
                                                        watchedCadence ??
                                                            'weekly'
                                                    )}
                                                </p>
                                                {watchedCadence === 'custom' &&
                                                    !showCustomRecurrence && (
                                                        <Button
                                                            variant="ghost"
                                                            size="sm"
                                                            onClick={() =>
                                                                setShowCustomRecurrence(
                                                                    true
                                                                )
                                                            }
                                                            className="mt-1 h-7 text-xs text-brand hover:text-brand-dark px-2"
                                                            type="button"
                                                        >
                                                            Edit pattern
                                                        </Button>
                                                    )}
                                                {showCustomRecurrence && (
                                                    <div className="mt-3 p-4 border border-gray-200 rounded-lg bg-gray-50 space-y-4">
                                                        <div className="flex items-center justify-between">
                                                            <Label className="text-sm font-medium text-gray-700">
                                                                Custom
                                                                Recurrence
                                                                Pattern
                                                            </Label>
                                                            <Button
                                                                variant="ghost"
                                                                size="sm"
                                                                onClick={() => {
                                                                    setShowCustomRecurrence(
                                                                        false
                                                                    );
                                                                    setValue(
                                                                        'cadence',
                                                                        'weekly',
                                                                        {
                                                                            shouldDirty: true,
                                                                            shouldValidate: true
                                                                        }
                                                                    );
                                                                }}
                                                                className="h-6 w-6 p-0 -mt-1"
                                                                type="button"
                                                            >
                                                                <X className="size-4" />
                                                            </Button>
                                                        </div>
                                                        <div>
                                                            <Label
                                                                htmlFor="customInterval"
                                                                className="text-xs"
                                                            >
                                                                Repeats every X
                                                                weeks
                                                            </Label>
                                                            <Select
                                                                value={String(
                                                                    customRecurrenceInterval
                                                                )}
                                                                onValueChange={(
                                                                    value
                                                                ) =>
                                                                    setCustomRecurrenceInterval(
                                                                        Number(
                                                                            value
                                                                        )
                                                                    )
                                                                }
                                                            >
                                                                <SelectTrigger
                                                                    id="customInterval"
                                                                    className={`bg-white ${focusRing}`}
                                                                >
                                                                    <SelectValue />
                                                                </SelectTrigger>
                                                                <SelectContent>
                                                                    {[
                                                                        1, 2, 3,
                                                                        4, 5, 6,
                                                                        7, 8
                                                                    ].map(
                                                                        (
                                                                            num
                                                                        ) => (
                                                                            <SelectItem
                                                                                key={
                                                                                    num
                                                                                }
                                                                                value={String(
                                                                                    num
                                                                                )}
                                                                            >
                                                                                {
                                                                                    num
                                                                                }{' '}
                                                                                {num ===
                                                                                1
                                                                                    ? 'week'
                                                                                    : 'weeks'}
                                                                            </SelectItem>
                                                                        )
                                                                    )}
                                                                </SelectContent>
                                                            </Select>
                                                            <p className="text-xs text-gray-500 mt-1">
                                                                Use the "Days of
                                                                Week" selector
                                                                below to choose
                                                                which days
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
                                                                    Please
                                                                    select at
                                                                    least one
                                                                    day of the
                                                                    week below
                                                                </span>
                                                            </div>
                                                        )}
                                                        <div className="flex gap-2 pt-2">
                                                            <Button
                                                                variant="outline"
                                                                size="sm"
                                                                onClick={() => {
                                                                    setShowCustomRecurrence(
                                                                        false
                                                                    );
                                                                    setValue(
                                                                        'cadence',
                                                                        'weekly',
                                                                        {
                                                                            shouldDirty: true,
                                                                            shouldValidate: true
                                                                        }
                                                                    );
                                                                }}
                                                                className="flex-1"
                                                                type="button"
                                                            >
                                                                Cancel
                                                            </Button>
                                                            <Button
                                                                size="sm"
                                                                onClick={() => {
                                                                    if (
                                                                        isCustomRecurrenceValid()
                                                                    ) {
                                                                        setValue(
                                                                            'cadence',
                                                                            'custom',
                                                                            {
                                                                                shouldDirty: true,
                                                                                shouldValidate: true
                                                                            }
                                                                        );
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
                                                                type="button"
                                                            >
                                                                Apply Pattern
                                                            </Button>
                                                        </div>
                                                    </div>
                                                )}
                                            </FormItem>
                                        )}
                                    />
                                    {(watchedCadence === 'weekly' ||
                                        watchedCadence === 'biweekly' ||
                                        watchedCadence === 'custom') && (
                                        <FormField
                                            control={control}
                                            name="days"
                                            render={() => (
                                                <FormItem className="!mt-0">
                                                    <FormLabel>
                                                        Days of Week *
                                                    </FormLabel>
                                                    <div className="flex flex-wrap gap-2 mt-2">
                                                        {WEEKDAY_OPTIONS.map(
                                                            (day) => (
                                                                <button
                                                                    key={
                                                                        day.value
                                                                    }
                                                                    type="button"
                                                                    onClick={() =>
                                                                        toggleDay(
                                                                            day.value
                                                                        )
                                                                    }
                                                                    className={`px-4 py-2 rounded-lg border transition-colors ${
                                                                        watchedDays.includes(
                                                                            day.value
                                                                        )
                                                                            ? 'bg-brand text-white border-brand'
                                                                            : 'bg-white text-gray-700 border-gray-300 hover:border-brand'
                                                                    } ${focusRingButton}`}
                                                                >
                                                                    {day.label.slice(
                                                                        0,
                                                                        3
                                                                    )}
                                                                </button>
                                                            )
                                                        )}
                                                    </div>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />
                                    )}
                                </div>
                            </div>

                            <div className="flex justify-end gap-3 pt-4">
                                <Button
                                    variant="outline"
                                    onClick={() => onCancel?.()}
                                    className={`border-gray-300 ${focusRingButton}`}
                                    type="button"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    className={`bg-brand hover:bg-brand-dark text-white ${focusRingButton}`}
                                    type="submit"
                                    disabled={isSubmitting}
                                >
                                    {isSubmitting
                                        ? 'Saving...'
                                        : 'Create Class'}
                                </Button>
                            </div>
                        </div>
                    )}

                    {!embedded && (
                        <>
                            <div className="section-card">
                                <h2 className="text-lg font-semibold text-foreground">
                                    Class Information
                                </h2>

                                {isNewClass && parentClassSelector}

                                <FormField
                                    control={control}
                                    name="description"
                                    render={({ field }) => (
                                        <FormItem className="space-y-2">
                                            <FormLabel>Description</FormLabel>
                                            <FormControl>
                                                <Textarea rows={3} {...field} />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />

                                <div className="grid grid-cols-2 gap-4">
                                    <FormField
                                        control={control}
                                        name="instructor_id"
                                        render={({ field }) => (
                                            <FormItem className="space-y-2">
                                                <FormLabel>
                                                    Instructor
                                                </FormLabel>
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
                                                            setShowAddInstructor(
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
                                                        <SelectTrigger>
                                                            <SelectValue placeholder="Select instructor" />
                                                        </SelectTrigger>
                                                    </FormControl>
                                                    <SelectContent>
                                                        {instructors.map(
                                                            (inst) => (
                                                                <SelectItem
                                                                    key={
                                                                        inst.id
                                                                    }
                                                                    value={String(
                                                                        inst.id
                                                                    )}
                                                                >
                                                                    {
                                                                        inst.name_last
                                                                    }
                                                                    ,{' '}
                                                                    {
                                                                        inst.name_first
                                                                    }
                                                                </SelectItem>
                                                            )
                                                        )}
                                                        <SelectItem
                                                            value="__add__"
                                                            className="text-brand font-medium"
                                                        >
                                                            + Add Instructor
                                                        </SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />

                                    <FormField
                                        control={control}
                                        name="capacity"
                                        render={({ field }) => (
                                            <FormItem className="space-y-2">
                                                <FormLabel>Capacity</FormLabel>
                                                <FormControl>
                                                    <Input
                                                        type="number"
                                                        min={1}
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>

                                <div className="grid grid-cols-2 gap-4">
                                    <FormField
                                        control={control}
                                        name="room_id"
                                        render={({ field }) => (
                                            <FormItem className="space-y-2">
                                                <FormLabel>Room</FormLabel>
                                                <Select
                                                    value={roomSelectValue}
                                                    onValueChange={(v) => {
                                                        if (v === '__add__') {
                                                            setShowAddRoom(
                                                                true
                                                            );
                                                            return;
                                                        }
                                                        setRoomSelectValue(v);
                                                        field.onChange(
                                                            Number(v)
                                                        );
                                                    }}
                                                >
                                                    <FormControl>
                                                        <SelectTrigger>
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

                            {isNewClass && (
                                <div className="section-card">
                                    <h2 className="text-lg font-semibold text-foreground">
                                        Scheduling
                                    </h2>

                                    <div className="grid grid-cols-2 gap-4">
                                        <FormField
                                            control={control}
                                            name="start_dt"
                                            render={({ field }) => (
                                                <FormItem className="space-y-2">
                                                    <FormLabel>
                                                        Start Date
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
                                            control={control}
                                            name="end_dt"
                                            render={({ field }) => (
                                                <FormItem className="space-y-2">
                                                    <FormLabel>
                                                        End Date
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
                                    </div>

                                    <div className="grid grid-cols-2 gap-4">
                                        <FormField
                                            control={control}
                                            name="start_time"
                                            render={({ field }) => (
                                                <FormItem className="space-y-2">
                                                    <FormLabel>
                                                        Start Time
                                                    </FormLabel>
                                                    <FormControl>
                                                        <Input
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
                                                <FormItem className="space-y-2">
                                                    <FormLabel>
                                                        End Time
                                                    </FormLabel>
                                                    <FormControl>
                                                        <Input
                                                            type="time"
                                                            {...field}
                                                        />
                                                    </FormControl>
                                                    <FormMessage />
                                                </FormItem>
                                            )}
                                        />
                                    </div>

                                    <FormField
                                        control={control}
                                        name="days"
                                        render={() => (
                                            <FormItem className="space-y-2">
                                                <FormLabel>
                                                    Days of Week *
                                                </FormLabel>
                                                <div className="flex flex-wrap gap-2">
                                                    {WEEKDAY_OPTIONS.map(
                                                        (day) => (
                                                            <Button
                                                                key={day.value}
                                                                type="button"
                                                                variant={
                                                                    watchedDays.includes(
                                                                        day.value
                                                                    )
                                                                        ? 'default'
                                                                        : 'outline'
                                                                }
                                                                size="sm"
                                                                className={
                                                                    watchedDays.includes(
                                                                        day.value
                                                                    )
                                                                        ? 'bg-brand hover:bg-brand-dark text-white'
                                                                        : 'border-gray-300'
                                                                }
                                                                onClick={() =>
                                                                    toggleDay(
                                                                        day.value
                                                                    )
                                                                }
                                                            >
                                                                {day.label.slice(
                                                                    0,
                                                                    3
                                                                )}
                                                            </Button>
                                                        )
                                                    )}
                                                </div>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                            )}

                            <div className="section-card">
                                <h2 className="text-lg font-semibold text-foreground">
                                    Status
                                </h2>
                                <FormField
                                    control={control}
                                    name="status"
                                    render={({ field }) => (
                                        <FormItem className="space-y-2">
                                            <FormLabel>Class Status</FormLabel>
                                            <Select
                                                value={field.value}
                                                onValueChange={field.onChange}
                                                disabled={blockEdits}
                                            >
                                                <FormControl>
                                                    <SelectTrigger>
                                                        <SelectValue />
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent>
                                                    {Object.entries(
                                                        statusOptions
                                                    ).map(([label, value]) => (
                                                        <SelectItem
                                                            key={value}
                                                            value={value}
                                                        >
                                                            {label}
                                                        </SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>

                            <div className="flex items-center justify-end gap-3">
                                <Button
                                    type="button"
                                    variant="outline"
                                    onClick={() => {
                                        if (onCancel) {
                                            onCancel();
                                            return;
                                        }
                                        navigate(`/programs/${programId}`);
                                    }}
                                    className="border-gray-300"
                                >
                                    Cancel
                                </Button>
                                <Button
                                    type="submit"
                                    disabled={isSubmitting}
                                    className="btn-gold-thin"
                                >
                                    {isSubmitting
                                        ? 'Saving...'
                                        : isNewClass
                                          ? 'Create Class'
                                          : 'Save Changes'}
                                </Button>
                            </div>
                        </>
                    )}
                </form>
            </Form>

            <FormModal
                open={showConflicts}
                onOpenChange={setShowConflicts}
                title="Scheduling Conflict"
                description={`${conflictSubjectLabel(conflicts)} has ${conflicts.length} ${conflicts.length === 1 ? 'conflict' : 'conflicts'} with existing classes.`}
            >
                <div className="max-h-[50vh] overflow-y-auto space-y-3 pr-1">
                    {conflicts.map((c, i) => (
                        <div
                            key={i}
                            className="bg-red-50 p-3 rounded-lg text-sm"
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
                <div className="flex justify-end pt-2">
                    <Button
                        variant="outline"
                        onClick={() => setShowConflicts(false)}
                    >
                        Close
                    </Button>
                </div>
            </FormModal>

            {!embedded && (
                <FormModal
                    open={showAddRoom}
                    onOpenChange={setShowAddRoom}
                    title="Add Room"
                >
                    <div className="space-y-4">
                        <div className="space-y-2">
                            <Label htmlFor="new-room-name">Room Name</Label>
                            <Input
                                id="new-room-name"
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
            )}

            {!embedded && (
                <FormModal
                    open={showAddInstructor}
                    onOpenChange={setShowAddInstructor}
                    title="Add Instructor"
                >
                    <div className="space-y-4">
                        <div className="grid grid-cols-2 gap-4">
                            <div className="space-y-2">
                                <Label htmlFor="inst-first">First Name</Label>
                                <Input
                                    id="inst-first"
                                    value={newInstructor.name_first}
                                    onChange={(e) =>
                                        setNewInstructor((p) => ({
                                            ...p,
                                            name_first: e.target.value
                                        }))
                                    }
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor="inst-last">Last Name</Label>
                                <Input
                                    id="inst-last"
                                    value={newInstructor.name_last}
                                    onChange={(e) =>
                                        setNewInstructor((p) => ({
                                            ...p,
                                            name_last: e.target.value
                                        }))
                                    }
                                />
                            </div>
                        </div>
                        <div className="space-y-2">
                            <Label htmlFor="inst-username">Username</Label>
                            <Input
                                id="inst-username"
                                value={newInstructor.username}
                                onChange={(e) =>
                                    setNewInstructor((p) => ({
                                        ...p,
                                        username: e.target.value
                                    }))
                                }
                            />
                        </div>
                        <div className="flex justify-end gap-2">
                            <Button
                                variant="outline"
                                onClick={() => setShowAddInstructor(false)}
                            >
                                Cancel
                            </Button>
                            <Button
                                className="btn-brand-dark"
                                onClick={() => void handleAddInstructor()}
                            >
                                Create
                            </Button>
                        </div>
                    </div>
                </FormModal>
            )}
        </div>
    );
}

export default function ClassManagementForm() {
    const { id: programId, class_id } = useParams<{
        id: string;
        class_id?: string;
    }>();
    const loaderData = useLoaderData() as ClassLoaderData;

    if (!programId) {
        return null;
    }

    return (
        <ClassManagementFormInner
            programId={programId}
            classId={class_id}
            loaderData={loaderData}
        />
    );
}
