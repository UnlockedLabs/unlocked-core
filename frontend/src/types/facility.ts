export interface Facility {
    [key: string]: string | number;
    id: number;
    name: string;
    created_at: string;
    updated_at: string;
    timezone: string;
}

export interface Room {
    id: number;
    facility_id: number;
    name: string;
    created_at: string;
    updated_at: string;
}

export interface RoomConflict {
    conflicting_event_id: number;
    conflicting_class_id: number;
    class_name: string;
    start_time: string;
    end_time: string;
    conflict_type: 'room' | 'instructor';
}

export interface FacilityWithStats {
    id: number;
    name: string;
    created_at: string;
    updated_at: string;
    timezone: string;
    active_programs: number;
    active_classes: number;
    total_residents: number;
    /** True when the facility has no records that block deletion. */
    can_delete: boolean;
}

// Feature names are plain strings here: a product can register a feature
// that isn't in the FeatureAccess enum.
export interface FeatureCatalogEntry {
    name: string;
    title: string;
    description: string | null;
    /** Set only for a sub-feature. */
    parent?: string;
}

export interface FacilityFeatureOverviewRow {
    facility_id: number;
    facility_name: string;
    features: Partial<Record<string, boolean>>;
}

export interface FacilityFeatureDetailRow {
    feature: string;
    enabled: boolean;
}

export enum Timezones {
    'CST' = 'America/Chicago',
    'EST' = 'America/New_York',
    'AKST' = 'America/Anchorage',
    'PST' = 'America/Los_Angeles',
    'MDT' = 'America/Denver',
    'MST' = 'America/Phoenix'
}
