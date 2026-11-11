import { cn } from '@/lib/utils';
import {
    SelectedClassStatus,
    ProgClassStatus,
    ProgramEffectiveStatus,
    EnrollmentStatus
} from '@/types';

const classStatusStyles: Record<SelectedClassStatus, string> = {
    [SelectedClassStatus.Active]: 'bg-green-50 text-green-700 border-green-200',
    [SelectedClassStatus.Scheduled]: 'bg-blue-50 text-blue-700 border-blue-200',
    [SelectedClassStatus.Completed]: 'bg-muted text-foreground border-border',
    [SelectedClassStatus.Paused]: 'bg-amber-50 text-amber-700 border-amber-200',
    [SelectedClassStatus.Cancelled]: 'bg-red-50 text-red-700 border-red-200'
};

const progClassStatusStyles: Record<ProgClassStatus, string> = {
    [ProgClassStatus.ACTIVE]: 'bg-green-50 text-green-700 border-green-200',
    [ProgClassStatus.SCHEDULED]: 'bg-blue-50 text-blue-700 border-blue-200',
    [ProgClassStatus.COMPLETED]: 'bg-muted text-foreground border-border',
    [ProgClassStatus.PAUSED]: 'bg-amber-50 text-amber-700 border-amber-200',
    [ProgClassStatus.CANCELLED]: 'bg-red-50 text-red-700 border-red-200'
};

const programStatusStyles: Record<ProgramEffectiveStatus, string> = {
    [ProgramEffectiveStatus.Available]:
        'bg-green-100 text-green-700 border-green-300',
    [ProgramEffectiveStatus.Inactive]:
        'bg-muted text-foreground border-gray-300',
    [ProgramEffectiveStatus.Archived]: 'bg-red-100 text-red-700 border-red-300'
};

const enrollmentStatusStyles: Record<string, string> = {
    Enrolled: 'bg-green-50 text-green-700 border-green-200',
    Completed: 'bg-blue-50 text-blue-700 border-blue-200',
    Dropped: 'bg-red-50 text-red-700 border-red-200',
    Transferred: 'bg-amber-50 text-amber-700 border-amber-200'
};

const residentStatusStyles: Record<string, string> = {
    Active: 'bg-green-100 text-green-800 border-green-300',
    Inactive: 'bg-gray-100 text-gray-700 border-gray-300',
    Archived: 'bg-orange-100 text-orange-700 border-orange-300'
};

type StatusType =
    | SelectedClassStatus
    | ProgClassStatus
    | ProgramEffectiveStatus
    | EnrollmentStatus
    | string;

interface StatusBadgeProps {
    status: StatusType;
    variant?:
        | 'class'
        | 'progClass'
        | 'program'
        | 'enrollment'
        | 'resident'
        | 'auto';
    className?: string;
}

const STYLE_REGISTRY: {
    variant: StatusBadgeProps['variant'];
    styles: Record<string, string>;
}[] = [
    { variant: 'class', styles: classStatusStyles },
    {
        variant: 'progClass',
        styles: progClassStatusStyles
    },
    {
        variant: 'program',
        styles: programStatusStyles
    },
    { variant: 'enrollment', styles: enrollmentStatusStyles },
    { variant: 'resident', styles: residentStatusStyles }
];

function getStyleForStatus(
    status: StatusType,
    variant: StatusBadgeProps['variant']
): string {
    const key = String(status);
    for (const entry of STYLE_REGISTRY) {
        if (
            (entry.variant === variant || variant === 'auto') &&
            key in entry.styles
        ) {
            return entry.styles[key];
        }
    }
    return 'bg-muted text-foreground border-border';
}

export function StatusBadge({
    status,
    variant = 'auto',
    className
}: StatusBadgeProps) {
    const style = getStyleForStatus(status, variant);
    return (
        <span
            className={cn(
                'inline-flex items-center text-xs font-medium px-2 py-0.5 rounded-full border whitespace-nowrap',
                style,
                className
            )}
        >
            {status}
        </span>
    );
}
