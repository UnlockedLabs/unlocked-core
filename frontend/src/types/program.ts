import { Facility } from './facility';
import { ProgramClassEvent } from './events';
import {
    SelectedClassStatus,
    EnrollmentStatus,
    ClassEnrollment
} from './attendance';
import { User } from './user';

export enum FundingType {
    EDUCATIONAL_GRANTS = 'Educational_Grants',
    FEDERAL_GRANTS = 'Federal_Grants',
    INMATE_WELFARE = 'Inmate_Welfare_Funds',
    NON_PROFIT_ORGANIZATION = 'Nonprofit_Organizations',
    STATE_GRANTS = 'State_Grants',
    OTHER = 'Other'
}

export enum ProgramType {
    EDUCATIONAL = 'Educational',
    LIFE_SKILLS = 'Life_Skills',
    MENTAL_HEALTH = 'Mental_Health_Behavioral',
    RELIGIOUS = 'Religious_Faith-Based',
    RE_ENTRY = 'Re-Entry',
    THERAPEUTIC = 'Therapeutic',
    VOCATIONAL = 'Vocational'
}

export enum CreditType {
    COMPLETION = 'Completion',
    EARNED_TIME = 'Earned-time',
    EDUCATION = 'Education',
    PARTICIPATION = 'Participation'
}

export enum ProgClassStatus {
    SCHEDULED = 'Scheduled',
    ACTIVE = 'Active',
    CANCELLED = 'Cancelled',
    COMPLETED = 'Completed',
    PAUSED = 'Paused'
}

export enum ProgramEffectiveStatus {
    Available = 'Available',
    Inactive = 'Inactive',
    Archived = 'Archived'
}

export type ProgramAction =
    | 'set_available'
    | 'set_inactive'
    | 'archive'
    | 'reactivate';

export interface ProgramCreditType {
    credit_type: CreditType;
    program_id?: number;
}

export interface PgmType {
    program_type: ProgramType;
    program_id?: number;
}

export interface ProgramTag {
    id: string;
    value: number;
}

export interface Program {
    id: number;
    created_at: Date;
    updated_at: Date;
    name: string;
    description: string;
    credit_types: ProgramCreditType[];
    funding_type: FundingType;
    program_types: PgmType[];
    is_active: boolean;
    has_program_completion: boolean;
    tags: ProgramTag[];
    is_favorited: boolean;
    facilities: Facility[];
    archived_at: string;
}

export interface ProgramOverview extends Program {
    active_residents: number;
    active_enrollments: number;
    completions: number;
    total_enrollments: number;
    completion_rate: number | null;
    attendance_rate: number | null;
    active_class_facility_ids: number[];
    source?: string;
    loading?: boolean;
}

export interface ProgramClassOutcomes {
    month: string;
    completions: number;
    drops: number;
}

export interface ProgramsFacilitiesStats {
    total_programs: number;
    avg_active_programs_per_facility: number;
    total_enrollments: number;
    attendance_rate: number;
    completion_rate: number;
}

export interface ProgramsOverviewTable {
    program_id: number;
    program_name: string;
    description: string;
    archived_at: string;
    total_active_facilities: number;
    /** The facilities total_active_facilities counts, sorted. Only sent to
     *  admins who see that count. */
    facility_names?: string[];
    total_enrollments: number;
    total_active_enrollments: number;
    total_classes: number;
    total_active_classes: number;
    total_cancelled_classes: number;
    total_capacity: number;
    total_filled_seats: number;
    completion_rate: number | null;
    attendance_rate: number | null;
    /** Numerator and denominator completion_rate was built from. Aggregate these
     *  across programs rather than reconstructing a denominator from
     *  total_enrollments, which counts enrollments the rate excludes. */
    rate_eligible_completions: number | null;
    rate_eligible_enrollments: number | null;
    program_types: string;
    credit_types: string;
    funding_type: string;
    status: boolean;
    source?: string;
    loading?: boolean;
}

// Programs whose classes are read live from a provider platform rather than
// stored by UnlockEd. They cannot be edited, deleted or scheduled here.
export function isExternalSource(source?: string): boolean {
    return source === 'canvas' || source === 'essential_ed';
}

/**
 * Human-readable name of the provider a program or class was read from, for
 * badges and empty states. Falls back to Canvas so payloads written before
 * `source` existed keep their old wording.
 */
export function externalSourceLabel(source?: string): string {
    return source === 'essential_ed' ? 'Essential Education' : 'Canvas';
}

export interface ProgramsOverview {
    programs_facilities_stats: ProgramsFacilitiesStats;
    programs_table: ProgramsOverviewTable[];
}

export interface Class {
    id: number;
    program_id: number;
    facility_id: number;
    name: string;
    description: string | null;
    credit_hours: number | null;
    archived_at: string | null;
    created_at?: string;
    updated_at?: string;

    /** Rolled up across this class's cohorts by the API; never stored. */
    cohort_count: number;
    /**
     * Per-status cohort counts. The program page grouped cohorts by status before the
     * class tier existed; these keep that information on the class row.
     */
    active_cohorts: number;
    scheduled_cohorts: number;
    completed_cohorts: number;
    enrolled: number;
    capacity: number;
    completed: number;

    cohorts?: Cohort[];
    /**
     * An EMPTY array from a raw read means "inherit from the program". The detail
     * endpoint resolves inheritance for you, so what arrives here is what applies.
     */
    credit_types?: ClassCreditType[];
    program?: Program;
    facility?: Facility;
}

export interface ClassCreditType {
    program_class_id: number;
    credit_type: string;
}

export interface Cohort {
    id: number;
    program_class_id: number;
    program_id: number;
    facility_id: number;
    facility_name: string;
    facility?: Facility;
    instructor?: User | null;
    /**
     * The parent CLASS's name ("General Education Diploma (GED)") -- what the UI shows
     * wherever a class name is displayed. Required, not optional: every endpoint that
     * returns a cohort populates it, either by joining program_classes or, for Canvas
     * courses (which have no class-tier row), by setting it from the course title in
     * canvas_programs.go. An endpoint that forgets renders a BLANK name, so if you add
     * one, join it.
     */
    class_name: string;
    description: string;
    start_dt: string;
    end_dt: string;
    status: SelectedClassStatus;
    enrolled: number;
    completed: number;
    historical_enrollments?: number;
    /** Rate-eligible counts apply the same enrollment_ended_at predicate as the
     *  server's completion rate. Use these for any completion-rate arithmetic;
     *  `completed` and `historical_enrollments` are raw counts. */
    rate_eligible_completions?: number;
    rate_eligible_enrollments?: number;
    capacity: number;
    credit_hours: number;
    archived_at: string | null;
    enrollments?: ClassEnrollment[];
    events: ProgramClassEvent[];
    created_at: Date;
    program: Program;
    schedule?: string;
    room?: string;
    attendance_rate?: number | null;
    is_canvas?: boolean;
    source?: string;
    /** True for a placeholder row standing in for a provider still being read. */
    loading?: boolean;
    canvas_timezone?: string;
}

export interface MissingAttendanceItem {
    cohort_id: number;
    class_name: string;
    facility_name?: string;
    event_id: number;
    date: string;
    start_time: string;
}

export interface TodaysScheduleItem {
    cohort_id: number;
    class_name: string;
    instructor_name: string;
    facility_id: number;
    facility_name: string;
    event_id: number;
    date: string;
    start_time: string;
    room: string;
    has_attendance?: boolean;
    enrolled_count?: number;
}

export interface ClassMetrics {
    active_classes: number;
    scheduled_classes: number;
    total_enrollments: number;
    total_seats: number;
    attendance_concerns: number;
}

export interface FacilityHealthSummary {
    facility_id: number;
    facility_name: string;
    programs: number;
    active_classes: number;
    enrollment: number;
    missing_attendance: number;
    attendance_concerns: number;
}

export interface ResidentProgramOverview {
    program_name: string;
    class_name: string;
    status: ProgClassStatus;
    credit_types: string;
    program_id: number;
    cohort_id: number;
    enrollment_id: number;
    updated_at: string;
    enrollment_status?: EnrollmentStatus;
    start_date: string;
    end_date?: string;
    present_attendance?: number;
    absent_attendance?: number;
    attendance_percentage?: string;
    change_reason?: string;
    schedule?: string;
    is_canvas?: boolean;
    source?: string;
}

export interface ProgramCompletion {
    id: number;
    program_class_id: number;
    facility_name: string;
    credit_type: string;
    admin_email: string;
    program_owner?: string;
    program_name: string;
    program_id: number;
    class_name: string;
    class_start_dt: string;
    created_at: string;
    updated_at: string;
    deleted_at: string;
    enrolled_on_dt: string;
    user?: User;
}

export interface ConflictDetail {
    user_id: number;
    user_name: string;
    conflicting_class: string;
    conflict_start: string;
    conflict_end: string;
    conflict_days: string[];
}

export interface OverrideForm {
    start_time: string;
    date: string;
    override_type: string;
    program_name?: string;
    location?: string;
    override_rule?: string;
    is_cancelled?: boolean;
    duration?: string;
}

export enum ChangeReason {
    'Scheduling conflict' = 'Scheduling conflict',
    'Resource unavailable' = 'Resource unavailable',
    'Administrative change' = 'Administrative change',
    'Other' = 'Other'
}

export enum CancelEventReason {
    'Instructor unavailable' = 'Instructor unavailable',
    'Instructor illness' = 'Instructor illness',
    'Facility issue or lockdown' = 'Facility issue or lockdown',
    'Holiday or scheduled break' = 'Holiday or scheduled break',
    'Technology issue' = 'Technology issue',
    'Other (add note)' = 'Other (add note)'
}

export enum ClassStatusOptions {
    Complete = 'Complete',
    Pause = 'Pause',
    Cancel = 'Cancel',
    Schedule = 'Schedule',
    Active = 'Active'
}

export enum ClassMgmtTabs {
    CLASS = 'Dashboard',
    SCHEDULE = 'Schedule',
    ENROLLMENT = 'Enrollment',
    ATTENDANCE = 'Attendance'
}

export enum FilterProgramClassEnrollments {
    'Last Name (A to Z)' = 'name_last asc',
    'Last Name (Z to A)' = 'name_last desc',
    'First Name (A to Z)' = 'name_first asc',
    'First Name (Z to A)' = 'name_first desc'
}
