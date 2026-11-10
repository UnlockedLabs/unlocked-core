import { z } from 'zod';
import { UserRole } from '@/types/user';
import {
    Attendance,
    AttendanceReason,
    ProgramType,
    CreditType,
    FundingType,
    ProgramEffectiveStatus,
    CancelEventReason
} from '@/types';

/**
 * Central form-validation module.
 *
 * Everything the app uses for form validation lives here: the shared message
 * catalog (`VMSG`), the reusable Zod field builders (`requiredString`, etc.),
 * and one schema (or factory) per form. Components import the schema + inferred
 * input type they need directly from `@/lib/validation`.
 *
 * Conventions:
 *  - Radix Select values are always strings; coerce ids to numbers at
 *    payload-build time.
 *  - Controlled inputs emit `''` (not `undefined`) when empty.
 */

/* ------------------------------------------------------------------ *
 * Messages
 * ------------------------------------------------------------------ */

/** Canonical user-facing validation wording, kept here so forms read consistently. */
export const VMSG = {
    required: (label: string) => `${label} is required`,
    maxLen: (label: string, n: number) =>
        `${label} must be ${n} characters or fewer`,
    minLen: (label: string, n: number) =>
        `${label} must be at least ${n} characters`,
    selectOne: (label: string) => `Select at least one ${label}`,
    selectField: (label: string) => `Please select ${label}`,
    passwordLength: 'Password must be at least 8 characters',
    passwordNumber: 'Password must include at least one number',
    passwordsMatch: 'Passwords do not match'
} as const;

/* ------------------------------------------------------------------ *
 * Reusable field builders (primitives)
 * ------------------------------------------------------------------ */

/** A trimmed, non-empty string with a labeled "required" + max-length message. */
export const requiredString = (label: string, max = 255) =>
    z
        .string()
        .trim()
        .min(1, VMSG.required(label))
        .max(max, VMSG.maxLen(label, max));

/** A trimmed, optional string (empty string allowed for controlled inputs). */
export const optionalString = (label = 'Field', max = 255) =>
    z
        .string()
        .trim()
        .max(max, VMSG.maxLen(label, max))
        .optional()
        .or(z.literal(''));

/** A non-empty array (e.g. a checkbox group requiring at least one choice). */
export const nonEmptyArray = <T extends z.ZodTypeAny>(item: T, label: string) =>
    z.array(item).min(1, VMSG.selectOne(label));

export const confirmField = (
    expected: string,
    message = 'Entry does not match'
) =>
    z
        .string()
        .refine((v) => expected.length > 0 && v === expected, { message });

/* ------------------------------------------------------------------ *
 * Auth — login
 * ------------------------------------------------------------------ */

/**
 * Login form. Only `identifier`/`password` are user-validated; the remaining
 * fields are hidden auth-flow values carried through to the API unchanged.
 */
export const loginSchema = z.object({
    identifier: z.string().trim().min(1, VMSG.required('Username')),
    password: z.string().min(1, VMSG.required('Password')),
    flow_id: z.string(),
    challenge: z.string(),
    csrf_token: z.string()
});

export type LoginInput = z.infer<typeof loginSchema>;

/* ------------------------------------------------------------------ *
 * Auth — change/reset password
 * ------------------------------------------------------------------ */

/**
 * Reset/change-password form. A factory because `facility_name`/`timezone` are
 * only required on a system admin's first login. The password-match rule is
 * attached to `confirm` so it shows inline.
 */
export const buildChangePasswordSchema = (isFirstLogin: boolean) =>
    z
        .object({
            password: z
                .string()
                .min(8, VMSG.passwordLength)
                .regex(/\d/, VMSG.passwordNumber),
            confirm: z.string().min(1, VMSG.required('Confirm password')),
            facility_name: isFirstLogin
                ? z
                      .string()
                      .trim()
                      .min(3, VMSG.minLen('Facility name', 3))
                      .max(50, VMSG.maxLen('Facility name', 50))
                : z.string(),
            timezone: isFirstLogin
                ? z.string().min(1, VMSG.required('Timezone'))
                : z.string()
        })
        .refine((data) => data.password === data.confirm, {
            message: VMSG.passwordsMatch,
            path: ['confirm']
        });

export interface ChangePasswordInput {
    password: string;
    confirm: string;
    facility_name: string;
    timezone: string;
}

/* ------------------------------------------------------------------ *
 * Admin — add / edit admin
 * ------------------------------------------------------------------ */

/**
 * Add Admin. Facility is required whenever the current user can switch
 * facilities (the dropdown is shown for every role; enforced via superRefine so
 * it surfaces inline). `facility_id` is a Radix Select string; coerce at payload.
 */
export const buildAdminAddSchema = (canSelectFacility: boolean) =>
    z
        .object({
            name_first: requiredString('First name'),
            name_last: requiredString('Last name'),
            username: requiredString('Username'),
            role: z.enum(UserRole),
            facility_id: z.string().optional()
        })
        .superRefine((data, ctx) => {
            if (canSelectFacility && !data.facility_id) {
                ctx.addIssue({
                    code: 'custom',
                    path: ['facility_id'],
                    message: VMSG.required('Facility')
                });
            }
        });

export type AdminAddInput = z.infer<ReturnType<typeof buildAdminAddSchema>>;

/**
 * Edit Admin — only name and (optionally) facility are editable; username/role
 * are read-only. `requireFacility` folds in both the can-switch check and role.
 */
export const buildAdminEditSchema = (requireFacility: boolean) =>
    z
        .object({
            name_first: requiredString('First name'),
            name_last: requiredString('Last name'),
            facility_id: z.string().optional()
        })
        .superRefine((data, ctx) => {
            if (requireFacility && !data.facility_id) {
                ctx.addIssue({
                    code: 'custom',
                    path: ['facility_id'],
                    message: VMSG.required('Facility')
                });
            }
        });

export type AdminEditInput = z.infer<ReturnType<typeof buildAdminEditSchema>>;

/**
 * Add Instructor, from the class forms. The same fields as Add Admin, without
 * role and facility: an instructor is a facility admin at the class's facility.
 */
export const addInstructorSchema = z.object({
    name_first: requiredString('First name'),
    name_last: requiredString('Last name'),
    username: requiredString('Username')
});

export type AddInstructorInput = z.infer<typeof addInstructorSchema>;

/* ------------------------------------------------------------------ *
 * Facility — add / edit
 * ------------------------------------------------------------------ */

/** Add/Edit Facility (shared). `timezone` is a required Radix Select string. */
export const facilitySchema = z.object({
    name: requiredString('Facility name'),
    timezone: requiredString('Timezone')
});

export type FacilityInput = z.infer<typeof facilitySchema>;

/* ------------------------------------------------------------------ *
 * Residents — add / edit / bulk / transfer / note
 * ------------------------------------------------------------------ */

/** "Add Resident" form. `facility_id` is a Radix Select string; coerce at payload. */
export const addResidentSchema = z.object({
    name_first: requiredString('First name', 50),
    name_last: requiredString('Last name', 50),
    username: requiredString('Username', 50),
    doc_id: requiredString('Resident ID', 32),
    facility_id: z.string().optional()
});

export type AddResidentInput = z.infer<typeof addResidentSchema>;

/** "Edit Resident" dialog. `username` is read-only; `doc_id` is required (ID-835). */
export const editResidentSchema = z.object({
    name_first: requiredString('First name', 50),
    name_last: requiredString('Last name', 50),
    doc_id: requiredString('Resident ID', 32)
});

export type EditResidentInput = z.infer<typeof editResidentSchema>;

/** Type-to-confirm deactivate/delete dialogs — type the resident ID exactly. */
export const buildConfirmResidentIdSchema = (
    expected: string,
    label = 'Resident ID'
) =>
    z.object({
        confirm: confirmField(expected, `Entry does not match the ${label}`)
    });

export type ConfirmResidentIdInput = z.infer<
    ReturnType<typeof buildConfirmResidentIdSchema>
>;

/** "Transfer Resident" dialog — choose a target facility and confirm the ID. */
export const buildTransferResidentSchema = (
    expected: string,
    label = 'Resident ID'
) =>
    z.object({
        facility_id: z.string().min(1, VMSG.selectField('a facility')),
        confirm: confirmField(expected, `Entry does not match the ${label}`)
    });

export type TransferResidentInput = z.infer<
    ReturnType<typeof buildTransferResidentSchema>
>;

/** Bulk type-to-confirm dialogs (reset password / deactivate / delete) — type the count. */
export const buildConfirmCountSchema = (expected: string) =>
    z.object({
        confirm: confirmField(expected, 'Entry does not match the number shown')
    });

export type ConfirmCountInput = z.infer<
    ReturnType<typeof buildConfirmCountSchema>
>;

/** "Add Historical Note" dialog — required note (trimmed value is sent). */
export const residentNoteSchema = z.object({
    note: requiredString('Note', 10000)
});

export type ResidentNoteInput = z.infer<typeof residentNoteSchema>;

/* ------------------------------------------------------------------ *
 * Programs — create page form / management form / edit dialog / type-to-confirm
 * ------------------------------------------------------------------ */

/**
 * Inline "Add Program" create form on ProgramsPage. Field names mirror the
 * form state (`types`/`creditTypes`/`fundingTypes`); the payload is mapped in
 * the submit handler. `fundingTypes` is a non-empty array though the UI is a
 * single select. `facilities` is not required.
 */
export const programCreateSchema = z.object({
    name: requiredString('Program name'),
    description: optionalString('Description', 1000),
    types: nonEmptyArray(z.enum(ProgramType), 'program type'),
    creditTypes: nonEmptyArray(z.enum(CreditType), 'credit type'),
    fundingTypes: nonEmptyArray(z.enum(FundingType), 'funding type'),
    status: z.enum(ProgramEffectiveStatus),
    hasProgramCompletion: z.boolean(),
    facilities: z.array(z.number())
});

export type ProgramCreateInput = z.infer<typeof programCreateSchema>;

/** Create/edit Program management form (`is_active` boolean variant). */
export const programFormSchema = z.object({
    name: requiredString('Program name'),
    description: requiredString('Description'),
    program_types: nonEmptyArray(z.enum(ProgramType), 'program type'),
    credit_types: nonEmptyArray(z.enum(CreditType), 'credit type'),
    funding_type: z.enum(FundingType, {
        message: VMSG.required('Funding type')
    }),
    is_active: z.boolean(),
    has_program_completion: z.boolean(),
    facility_ids: z.array(z.number())
});

export type ProgramFormInput = z.infer<typeof programFormSchema>;

/**
 * Program-detail "Edit Program" dialog. Mirrors the management form but tracks
 * availability as a three-way `status` enum and stores facility ids under
 * `facilities`.
 */
export const editProgramSchema = z.object({
    name: requiredString('Program name'),
    description: optionalString('Description'),
    program_types: nonEmptyArray(z.enum(ProgramType), 'program type'),
    credit_types: nonEmptyArray(z.enum(CreditType), 'credit type'),
    funding_type: z.enum(FundingType, {
        message: VMSG.required('Funding type')
    }),
    status: z.enum(['Available', 'Inactive', 'Archived']),
    facilities: z.array(z.number())
});

export type EditProgramInput = z.infer<typeof editProgramSchema>;

/**
 * Program "type-to-confirm" dialogs (Archive / Delete) — type the program name
 * exactly. (Distinct from the class-detail `typeToConfirmSchema`, which matches
 * a different token and uses a different message.)
 */
export const programTypeToConfirmSchema = (expected: string) =>
    z.object({
        confirmation: confirmField(
            expected,
            'Type the program name exactly to confirm'
        )
    });

export type ProgramTypeToConfirmInput = z.infer<
    ReturnType<typeof programTypeToConfirmSchema>
>;

/* ------------------------------------------------------------------ *
 * Classes — create/edit forms
 * ------------------------------------------------------------------ */

const cadence = z.enum([
    'no-repeat',
    'daily',
    'weekly',
    'biweekly',
    'monthly',
    'custom'
]);

/** Cadences that require at least one day of the week to be selected. */
const DAY_REQUIRED_CADENCES = ['weekly', 'biweekly', 'custom'];

function endTimeIsAfterStart(startTime: string, endTime: string): boolean {
    const [sh, sm] = startTime.split(':').map(Number);
    const [eh, em] = endTime.split(':').map(Number);
    if (!Number.isFinite(sh) || !Number.isFinite(sm)) return true;
    if (!Number.isFinite(eh) || !Number.isFinite(em)) return true;
    return eh * 60 + em > sh * 60 + sm;
}

/**
 * Factory for ClassManagementForm. When `isNewClass` the schedule is being
 * created, so start/end time, room, days (for repeating cadences) and the
 * end-after-start rule apply. When editing, only the core details are validated.
 */
export const buildClassManagementSchema = (isNewClass: boolean) =>
    z
        .object({
            description: requiredString('Description'),
            instructor_id: z
                .number({ message: VMSG.required('Instructor') })
                .nullable()
                .refine((v) => v != null, VMSG.required('Instructor')),
            capacity: z
                .number({ message: VMSG.required('Capacity') })
                .min(1, 'Minimum 1'),
            credit_hours: z.number().min(0).nullable(),
            start_dt: requiredString('Start date'),
            end_dt: z.string().optional().or(z.literal('')),
            status: requiredString('Status'),
            room_id: z
                .number({ message: VMSG.required('Room') })
                .nullable()
                .refine((v) => v != null, VMSG.required('Room')),
            start_time: z.string(),
            end_time: z.string(),
            days: z.array(z.string()),
            cadence
        })
        .superRefine((data, ctx) => {
            if (!isNewClass) return;
            if (!data.start_time) {
                ctx.addIssue({
                    code: 'custom',
                    path: ['start_time'],
                    message: VMSG.required('Start time')
                });
            }
            if (!data.end_time) {
                ctx.addIssue({
                    code: 'custom',
                    path: ['end_time'],
                    message: VMSG.required('End time')
                });
            }
            if (
                data.start_time &&
                data.end_time &&
                !endTimeIsAfterStart(data.start_time, data.end_time)
            ) {
                ctx.addIssue({
                    code: 'custom',
                    path: ['end_time'],
                    message: 'End time must be after start time'
                });
            }
            if (
                DAY_REQUIRED_CADENCES.includes(data.cadence) &&
                data.days.length === 0
            ) {
                ctx.addIssue({
                    code: 'custom',
                    path: ['days'],
                    message: VMSG.required('Days of week')
                });
            }
        });

export type ClassManagementInput = z.infer<
    ReturnType<typeof buildClassManagementSchema>
>;

/**
 * Raw form-values type (pre-validation). Differs from `ClassManagementInput`
 * because the required-instructor/room refinements narrow null out of the
 * parsed output, so the form is typed with this as `TFieldValues` and
 * `ClassManagementInput` as the transformed (post-resolve) values.
 */
export type ClassManagementFormValues = z.input<
    ReturnType<typeof buildClassManagementSchema>
>;

/**
 * EditClassModal. Scheduling times/room are optional (existing recurrence is
 * reused when blank); the day selection lives in component state.
 */
export const editClassSchema = z.object({
    class_name: requiredString('Class name'),
    description: requiredString('Description'),
    instructor_id: z.number().nullable(),
    capacity: z
        .number({ message: VMSG.required('Capacity') })
        .min(1, 'Minimum 1'),
    credit_hours: z.number().min(0, 'Minimum 0').nullable(),
    start_dt: requiredString('Start date'),
    end_dt: optionalString('End date'),
    room_id: z.number().nullable(),
    start_time: z.string(),
    end_time: z.string(),
    cadence: z.string(),
    status: requiredString('Status')
});

export type EditClassInput = z.infer<typeof editClassSchema>;

/* ------------------------------------------------------------------ *
 * Class detail modals
 * ------------------------------------------------------------------ */

/**
 * "Bulk change a session field" modal (room / instructor). A field option must
 * be selected; the reason is optional. `applyToFuture` is parent-owned.
 */
export const bulkSessionFieldSchema = z.object({
    selectedId: z.string().trim().min(1, VMSG.selectField('an option')),
    reason: optionalString('Reason')
});

export type BulkSessionFieldInput = z.infer<typeof bulkSessionFieldSchema>;

/** "Change class status" modal — a status is always selected. */
export const changeClassStatusSchema = z.object({
    status: z.string().trim().min(1, VMSG.required('Status'))
});

export type ChangeClassStatusInput = z.infer<typeof changeClassStatusSchema>;

/**
 * "Reschedule session" (class-detail) modal. Every field is individually
 * optional, but at least one change must be supplied.
 */
export const rescheduleSessionSchema = z
    .object({
        newDate: optionalString('Date'),
        newStartTime: optionalString('Start time'),
        newEndTime: optionalString('End time'),
        newRoom: z.string().optional().or(z.literal('')),
        instructor_id: z.string().optional().or(z.literal(''))
    })
    .refine(
        (v) =>
            [
                v.newDate,
                v.newStartTime,
                v.newEndTime,
                v.newRoom,
                v.instructor_id
            ].some((val) => (val ?? '').length > 0),
        {
            message:
                'Change at least one of date, start time, end time, room, or instructor',
            path: ['newDate']
        }
    );

export type RescheduleSessionInput = z.infer<typeof rescheduleSessionSchema>;

/**
 * "Change enrollment status" modal. The reason-required and status-must-differ
 * rules are layered on per-render in the modal via the runtime status.
 */
export const changeEnrollmentStatusSchema = z.object({
    status: z.string().trim().min(1, VMSG.required('Status')),
    reason: optionalString('Reason')
});

export type ChangeEnrollmentStatusInput = z.infer<
    typeof changeEnrollmentStatusSchema
>;

/**
 * "Edit enrollment date" modal. Date is required; the in-range bounds depend on
 * runtime values and are layered on with `.superRefine` from the modal.
 */
export const editEnrollmentDateSchema = z.object({
    newDate: z.string().trim().min(1, VMSG.selectField('an enrollment date'))
});

export type EditEnrollmentDateInput = z.infer<typeof editEnrollmentDateSchema>;

/**
 * "Bulk cancel sessions" modal. A reason is required; when the reason is
 * "other" a free-text note is required as well.
 */
export const bulkCancelSessionsSchema = z
    .object({
        reason: z.string().trim().min(1, VMSG.required('Reason')),
        note: optionalString('Note')
    })
    .refine((v) => v.reason !== 'other' || (v.note?.trim().length ?? 0) > 0, {
        message: 'Please specify a reason',
        path: ['note']
    });

export type BulkCancelSessionsInput = z.infer<typeof bulkCancelSessionsSchema>;

/**
 * Type-to-confirm schema factory (delete class / unenroll resident). The typed
 * value must exactly match the expected token.
 */
export const typeToConfirmSchema = (expected: string) =>
    z.object({
        confirmation: confirmField(expected)
    });

export type TypeToConfirmInput = z.infer<
    ReturnType<typeof typeToConfirmSchema>
>;

/* ------------------------------------------------------------------ *
 * Class enrollments
 * ------------------------------------------------------------------ */

/** "Add Class Enrollments" — at least one resident must be selected. */
export const classEnrollmentsSchema = z.object({
    user_ids: nonEmptyArray(z.number(), 'resident')
});

export type ClassEnrollmentsInput = z.infer<typeof classEnrollmentsSchema>;

/**
 * "Confirm Enrollment Action" reason dialog. Only opens for statuses that
 * require a reason, so the reason is always required here.
 */
export const enrollmentReasonSchema = z.object({
    reason: requiredString('Reason')
});

export type EnrollmentReasonInput = z.infer<typeof enrollmentReasonSchema>;

/* ------------------------------------------------------------------ *
 * Schedule modals
 * ------------------------------------------------------------------ */

/**
 * "Reschedule Series" modal. The recurrence pattern is validated imperatively
 * by `RRuleControl`, so only the (optional) room Select lives in the form.
 */
export const rescheduleSeriesSchema = z.object({
    room_id: z.string().optional(),
    instructor_id: z.string().optional()
});

export type RescheduleSeriesInput = z.infer<typeof rescheduleSeriesSchema>;

/**
 * "Reschedule Class" (single session, schedule view) modal. Date is required;
 * start/end time and room are optional (blank keeps current values). When both
 * times are provided, end must be after start.
 */
export const scheduleRescheduleSessionSchema = z
    .object({
        date: z.string().trim().min(1, VMSG.required('Date')),
        startTime: z.string(),
        endTime: z.string(),
        room_id: z.string().optional(),
        instructor_id: z.string().optional()
    })
    .refine((v) => !v.startTime || !v.endTime || v.endTime > v.startTime, {
        message: 'End time must be after start time',
        path: ['endTime']
    });

export type ScheduleRescheduleSessionInput = z.infer<
    typeof scheduleRescheduleSessionSchema
>;

/**
 * "Cancel Class" modal. A reason is required; when the reason is
 * "Other (add note)" a custom note is required.
 */
export const cancelEventSchema = z
    .object({
        reason: z.string().min(1, VMSG.selectField('a reason')),
        customReason: z.string().trim().max(255).optional().or(z.literal('')),
        applyToFuture: z.boolean()
    })
    .superRefine((v, ctx) => {
        if (
            v.reason === (CancelEventReason['Other (add note)'] as string) &&
            !v.customReason?.trim()
        ) {
            ctx.addIssue({
                code: 'custom',
                message: 'Please provide details for the cancellation',
                path: ['customReason']
            });
        }
    });

export type CancelEventInput = z.infer<typeof cancelEventSchema>;

/**
 * "Cancel Classes by Instructor" (bulk) modal. Instructor, date range and a
 * reason are required; a custom note is required when reason is
 * "Other (add note)". The end date must be on/after the start date.
 */
export const bulkCancelClassesSchema = z
    .object({
        instructorId: z.string().min(1, VMSG.required('Instructor')),
        startDate: z.string().min(1, VMSG.required('Start date')),
        endDate: z.string().min(1, VMSG.required('End date')),
        reason: z.string().min(1, VMSG.selectField('a reason')),
        customReason: z.string().trim().max(255).optional().or(z.literal(''))
    })
    .refine((v) => !v.startDate || !v.endDate || v.endDate >= v.startDate, {
        message: 'End date must be on or after the start date',
        path: ['endDate']
    })
    .superRefine((v, ctx) => {
        if (
            v.reason === (CancelEventReason['Other (add note)'] as string) &&
            !v.customReason?.trim()
        ) {
            ctx.addIssue({
                code: 'custom',
                message: 'Please provide details for the cancellation',
                path: ['customReason']
            });
        }
    });

export type BulkCancelClassesInput = z.infer<typeof bulkCancelClassesSchema>;

/** "Change Room" modal. A new room is required; the reason is optional. */
export const changeRoomSchema = z.object({
    room_id: z.string().min(1, VMSG.selectField('a room')),
    reason: z.string().optional(),
    applyToFuture: z.boolean()
});

export type ChangeRoomInput = z.infer<typeof changeRoomSchema>;

/** "Change Instructor" modal. A new instructor is required; reason optional. */
export const changeInstructorSchema = z.object({
    instructor_id: z.string().min(1, VMSG.selectField('an instructor')),
    reason: z.string().optional(),
    applyToFuture: z.boolean()
});

export type ChangeInstructorInput = z.infer<typeof changeInstructorSchema>;

/* ------------------------------------------------------------------ *
 * Knowledge Center — add video / add link
 * ------------------------------------------------------------------ */

/** "Add YouTube Video" dialog — a single required, valid URL. */
export const addVideoSchema = z.object({
    url: requiredString('URL').pipe(z.url('Enter a valid URL'))
});

export type AddVideoInput = z.infer<typeof addVideoSchema>;

/** "Add Helpful Link" dialog — title, valid URL, and description all required. */
export const addLinkSchema = z.object({
    title: requiredString('Title'),
    url: requiredString('URL').pipe(z.url('Enter a valid URL')),
    description: requiredString('Description')
});

export type AddLinkInput = z.infer<typeof addLinkSchema>;

/** "Request Content" dialog — a resident describes what they'd like added. */
export const requestContentSchema = z.object({
    content: requiredString('Request', 2000)
});

export type RequestContentInput = z.infer<typeof requestContentSchema>;

/* ------------------------------------------------------------------ *
 * Take Attendance grid (per-row validation, not a single RHF form)
 * ------------------------------------------------------------------ */

/**
 * Per-row validation for the "Take Attendance" grid. Rows are validated
 * individually and issues surfaced inline under each field.
 *  - Present / Partial: clock-in required; Partial also requires clock-out.
 *  - Absent (Excused / Unexcused): a reason is required.
 *  - Any row whose reason is "Other": a note is required.
 */
export const attendanceRowSchema = z
    .object({
        status: z.string(),
        reason: z.string(),
        note: z.string(),
        check_in_at: z.string(),
        check_out_at: z.string()
    })
    .superRefine((row, ctx) => {
        const isPresent = row.status === (Attendance.Present as string);
        const isPartial = row.status === (Attendance.Partial as string);
        const isAbsent =
            row.status === (Attendance.Absent_Excused as string) ||
            row.status === (Attendance.Absent_Unexcused as string);

        if (!row.status) {
            ctx.addIssue({
                code: 'custom',
                path: ['status'],
                message: 'Select an attendance status'
            });
        }
        if ((isPresent || isPartial) && !row.check_in_at) {
            ctx.addIssue({
                code: 'custom',
                path: ['check_in_at'],
                message: 'Clock-in time is required'
            });
        }
        if (isPartial && !row.check_out_at) {
            ctx.addIssue({
                code: 'custom',
                path: ['check_out_at'],
                message: 'Clock-out time is required'
            });
        }
        if (isAbsent && !row.reason) {
            ctx.addIssue({
                code: 'custom',
                path: ['reason'],
                message: 'A reason is required'
            });
        }
        if (
            row.reason === (AttendanceReason.Other as string) &&
            !row.note.trim()
        ) {
            ctx.addIssue({
                code: 'custom',
                path: ['note'],
                message: 'Please specify a note'
            });
        }
    });

export type AttendanceRowErrors = Partial<
    Record<
        'status' | 'check_in_at' | 'check_out_at' | 'reason' | 'note',
        string
    >
>;

/** Validate a single attendance row; returns a field→message map ({} if valid). */
export function validateAttendanceRow(row: {
    status: string;
    reason: string;
    note: string;
    check_in_at: string;
    check_out_at: string;
}): AttendanceRowErrors {
    const result = attendanceRowSchema.safeParse(row);
    if (result.success) return {};
    const errors: AttendanceRowErrors = {};
    for (const issue of result.error.issues) {
        const key = issue.path[0] as keyof AttendanceRowErrors;
        if (key && !errors[key]) errors[key] = issue.message;
    }
    return errors;
}
