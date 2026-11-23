import { Link, useLocation } from 'react-router-dom';
import {
    useAuth,
    isAdministrator,
    hasFeature,
    canSwitchFacility,
    handleLogout,
    isKnowledgeCenterOnly
} from '@/auth/useAuth';
import { FeatureAccess } from '@/types';
import { cn } from '@/lib/utils';
import { useState } from 'react';
import Brand from '@/components/Brand';
import { Button } from '@/components/ui/button';
import { useTourContext } from '@/contexts/useTourContext';
import { LogOut } from 'lucide-react';
import {
    HomeIcon,
    AcademicCapIcon,
    RectangleStackIcon,
    UsersIcon,
    UserGroupIcon,
    BookOpenIcon,
    PresentationChartLineIcon,
    BuildingOfficeIcon,
    CalendarIcon,
    ChevronDownIcon,
    ChevronRightIcon,
    ArrowPathIcon,
    QuestionMarkCircleIcon,
    AdjustmentsHorizontalIcon,
    PencilSquareIcon,
    CircleStackIcon,
    ArrowDownTrayIcon
} from '@heroicons/react/24/outline';

interface SidebarProps {
    collapsed: boolean;
    onToggleCollapse: () => void;
    onNavigate?: () => void;
    onToggleHelpCenter?: () => void;
    showCollapseToggle?: boolean;
}

export default function Sidebar({
    collapsed,
    onToggleCollapse,
    onNavigate,
    onToggleHelpCenter,
    showCollapseToggle = true
}: SidebarProps) {
    const { user } = useAuth();
    const location = useLocation();

    if (!user) return null;

    const isActive = (paths: string[]) =>
        paths.some((p) => location.pathname.startsWith(p));

    return (
        <div
            className={cn(
                'bg-background border-r border-border transition-all duration-300 flex flex-col h-full',
                collapsed ? 'w-20' : 'w-64'
            )}
        >
            <div className="h-16 border-b border-border flex items-center justify-between px-4 shrink-0">
                {!collapsed && <Brand />}
                {showCollapseToggle && (
                    <button
                        onClick={onToggleCollapse}
                        className="p-2 rounded-lg hover:bg-accent transition-colors"
                        aria-label={
                            collapsed ? 'Expand sidebar' : 'Collapse sidebar'
                        }
                    >
                        <ChevronRightIcon
                            className={cn(
                                'size-5 text-muted-foreground transition-transform',
                                !collapsed && 'rotate-180'
                            )}
                        />
                    </button>
                )}
            </div>

            <nav className="p-3 space-y-1 overflow-y-auto flex-1">
                {isAdministrator(user) ? (
                    <AdminNav
                        collapsed={collapsed}
                        isActive={isActive}
                        onNavigate={onNavigate}
                    />
                ) : (
                    <StudentNav
                        collapsed={collapsed}
                        isActive={isActive}
                        onNavigate={onNavigate}
                        onToggleHelpCenter={onToggleHelpCenter}
                    />
                )}
            </nav>

            {!isAdministrator(user) && (
                <div className="border-t border-border p-4 shrink-0">
                    {!collapsed ? (
                        <div className="space-y-3">
                            <div className="flex items-center gap-3">
                                <div className="size-10 rounded-full bg-brand flex items-center justify-center text-white font-semibold flex-shrink-0">
                                    {user.name_first?.[0]}
                                    {user.name_last?.[0]}
                                </div>
                                <div className="flex-1 min-w-0">
                                    <p className="text-sm font-semibold text-foreground truncate">
                                        {user.name_first} {user.name_last}
                                    </p>
                                    <p className="text-xs text-muted-foreground">
                                        Resident
                                    </p>
                                </div>
                            </div>
                            <Button
                                variant="outline"
                                onClick={() => void handleLogout()}
                                className="w-full justify-start"
                            >
                                <LogOut className="size-4 mr-2" />
                                Log Out
                            </Button>
                        </div>
                    ) : (
                        <button
                            onClick={() => void handleLogout()}
                            className="w-full p-2 rounded-lg hover:bg-accent transition-colors flex items-center justify-center"
                            aria-label="Log out"
                        >
                            <LogOut className="size-5 text-muted-foreground" />
                        </button>
                    )}
                </div>
            )}
        </div>
    );
}

interface NavSectionProps {
    collapsed: boolean;
    isActive: (paths: string[]) => boolean;
    onNavigate?: () => void;
    onToggleHelpCenter?: () => void;
}

// Resident nav only. The AI Tutor MVP (ID-868) ships the resident-facing side
// alone — there is no staff experience behind this link — so admins get no
// entry point, and tutor-routes.tsx narrows the route to match. Admins still
// enable the feature itself from Feature Control.
function AiTutorNavItem({
    collapsed,
    isActive,
    onNavigate
}: Pick<NavSectionProps, 'collapsed' | 'isActive' | 'onNavigate'>) {
    const { user } = useAuth();
    if (!user || !hasFeature(user, FeatureAccess.AiTutorAccess)) return null;

    return (
        <NavLink
            to="/ai-tutor"
            icon={AcademicCapIcon}
            label="AI Tutor"
            active={isActive(['/ai-tutor'])}
            collapsed={collapsed}
            onClick={onNavigate}
        />
    );
}

function AdminNav({ collapsed, isActive, onNavigate }: NavSectionProps) {
    const { user } = useAuth();
    if (!user) return null;

    return (
        <>
            {!isKnowledgeCenterOnly(user) && (
                <NavLink
                    to="/dashboard"
                    icon={HomeIcon}
                    label="Dashboard"
                    active={isActive(['/dashboard'])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                />
            )}

            <SectionHeader label="Core" collapsed={collapsed} />

            <NavLink
                to="/residents"
                icon={UsersIcon}
                label="Residents"
                active={isActive(['/residents'])}
                collapsed={collapsed}
                onClick={onNavigate}
            />
            {canSwitchFacility(user) && (
                <>
                    <NavLink
                        to="/admins"
                        icon={UserGroupIcon}
                        label="Admins"
                        active={isActive(['/admins'])}
                        collapsed={collapsed}
                        onClick={onNavigate}
                    />
                    <NavLink
                        to="/facilities"
                        icon={BuildingOfficeIcon}
                        label="Facilities"
                        active={isActive(['/facilities'])}
                        collapsed={collapsed}
                        onClick={onNavigate}
                    />
                </>
            )}
            {canSwitchFacility(user) && (
                <NavLink
                    to="/feature-control"
                    icon={AdjustmentsHorizontalIcon}
                    label="Feature Control"
                    active={isActive(['/feature-control'])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                />
            )}
            <NavLink
                to="/operational-insights"
                icon={PresentationChartLineIcon}
                label="Insights"
                active={isActive(['/operational-insights'])}
                collapsed={collapsed}
                onClick={onNavigate}
            />
            {isAdministrator(user) &&
                hasFeature(user, FeatureAccess.ProgramAccess) && (
                    <NavLink
                        to="/exports"
                        icon={ArrowDownTrayIcon}
                        label="Exports"
                        active={isActive(['/exports'])}
                        collapsed={collapsed}
                        onClick={onNavigate}
                    />
                )}

            {hasFeature(user, FeatureAccess.ProgramAccess) && (
                <CollapsibleSection
                    id="program-management"
                    label="Program Management"
                    icon={AcademicCapIcon}
                    collapsed={collapsed}
                    isActive={isActive([
                        '/programs',
                        '/program-classes',
                        '/classes',
                        '/schedule'
                    ])}
                    onNavigate={onNavigate}
                    items={[
                        {
                            to: '/programs',
                            icon: RectangleStackIcon,
                            label: 'Programs',
                            active: isActive(['/programs'])
                        },
                        {
                            to: '/classes',
                            icon: AcademicCapIcon,
                            label: 'Classes',
                            active: isActive(['/classes', '/program-classes'])
                        },
                        {
                            to: '/schedule',
                            icon: CalendarIcon,
                            label: 'Schedule',
                            active: isActive(['/schedule'])
                        }
                    ]}
                />
            )}

            {isAdministrator(user) &&
                hasFeature(user, FeatureAccess.ProviderAccess) && (
                    <NavLink
                        to="/learning-platforms"
                        icon={CircleStackIcon}
                        label="Learning Platforms"
                        active={isActive([
                            '/learning-platforms',
                            '/provider-users'
                        ])}
                        collapsed={collapsed}
                        onClick={onNavigate}
                    />
                )}

            {hasFeature(user, FeatureAccess.OpenContentAccess) && (
                <CollapsibleSection
                    id="knowledge-center"
                    label="Knowledge Center"
                    icon={BookOpenIcon}
                    collapsed={collapsed}
                    isActive={isActive(['/knowledge-center-management'])}
                    onNavigate={onNavigate}
                    items={[
                        {
                            to: '/knowledge-center-management',
                            icon: BookOpenIcon,
                            label: 'Knowledge Center',
                            active: isActive(['/knowledge-center-management'])
                        }
                    ]}
                />
            )}
        </>
    );
}

function StudentNav({
    collapsed,
    isActive,
    onNavigate,
    onToggleHelpCenter
}: NavSectionProps) {
    const { user } = useAuth();
    const { tourState } = useTourContext();
    if (!user) return null;
    const hasOpen = hasFeature(user, FeatureAccess.OpenContentAccess);
    const hasLearningRecord = hasFeature(
        user,
        FeatureAccess.LearningRecordAccess
    );
    // Residents only see Programs when program tracking is on *and* the
    // resident-facing sub-feature hasn't been turned off for their facility.
    const hasProgram = hasFeature(
        user,
        FeatureAccess.ProgramAccess,
        FeatureAccess.ResidentProgramsAccess
    );
    const hasHome = hasOpen || hasLearningRecord;

    const tourHighlight = (target: string) =>
        tourState.tourActive && tourState.target === target
            ? 'animate-pulse border-2 border-brand-gold rounded-xl'
            : undefined;

    return (
        <>
            {hasHome ? (
                <NavLink
                    id="navigate-homepage"
                    to="/home"
                    icon={HomeIcon}
                    label="Home"
                    active={isActive(['/home'])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                    extraClassName={tourHighlight('#navigate-homepage')}
                />
            ) : (
                !hasProgram && (
                    <NavLink
                        to="/temp-home"
                        icon={HomeIcon}
                        label="Home"
                        active={isActive(['/temp-home'])}
                        collapsed={collapsed}
                        onClick={onNavigate}
                    />
                )
            )}
            {hasLearningRecord && (
                <NavLink
                    to="/learning-record-funnel"
                    icon={PencilSquareIcon}
                    label="Learning Record"
                    active={isActive([
                        '/learning-record-funnel',
                        '/learning-record-categories'
                    ])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                />
            )}
            {hasOpen && (
                <NavLink
                    id="visit-knowledge-center"
                    to="/knowledge-center"
                    icon={BookOpenIcon}
                    label="Knowledge Center"
                    active={isActive(['/knowledge-center'])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                    extraClassName={tourHighlight('#visit-knowledge-center')}
                />
            )}
            {hasProgram && (
                <NavLink
                    to="/resident-programs"
                    icon={ArrowPathIcon}
                    label="Programs"
                    active={isActive(['/resident-programs'])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                />
            )}
            {hasProgram && (
                <NavLink
                    to="/resident-schedule"
                    icon={CalendarIcon}
                    label="Schedule"
                    active={isActive(['/resident-schedule'])}
                    collapsed={collapsed}
                    onClick={onNavigate}
                />
            )}
            <AiTutorNavItem
                collapsed={collapsed}
                isActive={isActive}
                onNavigate={onNavigate}
            />
            <SectionHeader label="Help" collapsed={collapsed} />
            <NavButton
                icon={QuestionMarkCircleIcon}
                label="FAQ"
                collapsed={collapsed}
                onClick={onToggleHelpCenter}
            />
        </>
    );
}

function SectionHeader({
    label,
    collapsed
}: {
    label: string;
    collapsed: boolean;
}) {
    if (collapsed) return <div className="border-t border-border my-3" />;
    return (
        <div className="pt-4 pb-2 px-3">
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
                {label}
            </div>
        </div>
    );
}

interface NavLinkProps {
    id?: string;
    to: string;
    icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
    label: string;
    active: boolean;
    collapsed: boolean;
    onClick?: () => void;
    extraClassName?: string;
}

function NavLink({
    id,
    to,
    icon: Icon,
    label,
    active,
    collapsed,
    onClick,
    extraClassName
}: NavLinkProps) {
    return (
        <Link
            id={id}
            to={to}
            onClick={onClick}
            className={cn(
                'flex items-center gap-3 px-3 py-2.5 rounded-lg transition-colors',
                active
                    ? 'bg-brand text-white'
                    : 'text-gray-700 dark:text-gray-300 hover:bg-accent',
                extraClassName
            )}
        >
            <Icon className="size-5 shrink-0" />
            {!collapsed && <span className="text-sm">{label}</span>}
        </Link>
    );
}

interface NavButtonProps {
    icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
    label: string;
    collapsed: boolean;
    onClick?: () => void;
}

function NavButton({ icon: Icon, label, collapsed, onClick }: NavButtonProps) {
    return (
        <button
            onClick={onClick}
            className={cn(
                'w-full flex items-center gap-3 px-3 py-2.5 rounded-lg transition-colors',
                'text-gray-700 dark:text-gray-300 hover:bg-accent'
            )}
        >
            <Icon className="size-5 shrink-0" />
            {!collapsed && <span className="text-sm">{label}</span>}
        </button>
    );
}

interface CollapsibleItem {
    to: string;
    icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
    label: string;
    active: boolean;
}

interface CollapsibleSectionProps {
    id: string;
    label: string;
    icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
    collapsed: boolean;
    isActive: boolean;
    items: CollapsibleItem[];
    onNavigate?: () => void;
}

function CollapsibleSection({
    label,
    icon: Icon,
    collapsed,
    isActive,
    items,
    onNavigate
}: CollapsibleSectionProps) {
    const [expanded, setExpanded] = useState(isActive);

    if (items.length === 0) return null;

    return (
        <div>
            {!collapsed && (
                <div className="pt-4 pb-2 px-3">
                    <div className="text-xs uppercase tracking-wider text-muted-foreground">
                        {label}
                    </div>
                </div>
            )}
            {collapsed && <div className="border-t border-border my-3" />}

            <button
                onClick={() => setExpanded(!expanded)}
                className={cn(
                    'w-full flex items-center gap-3 px-3 py-2.5 rounded-lg transition-colors',
                    isActive
                        ? 'text-brand dark:text-[#8fb55e]'
                        : 'text-gray-700 dark:text-gray-300 hover:bg-accent'
                )}
            >
                <Icon className="size-5 shrink-0" />
                {!collapsed && (
                    <>
                        <span className="text-sm flex-1 text-left">
                            {label}
                        </span>
                        <ChevronDownIcon
                            className={cn(
                                'size-4 transition-transform',
                                expanded && 'rotate-180'
                            )}
                        />
                    </>
                )}
            </button>
            {!collapsed && expanded && (
                <div className="ml-4 mt-1 space-y-1 pl-3 border-l-2 border-border">
                    {items.map((item) => (
                        <Link
                            key={item.to}
                            to={item.to}
                            onClick={onNavigate}
                            className={cn(
                                'w-full flex items-center gap-3 px-3 py-2 rounded-lg transition-colors text-sm',
                                item.active
                                    ? 'bg-brand/10 dark:bg-brand/20 text-brand dark:text-[#8fb55e]'
                                    : 'text-gray-600 dark:text-gray-400 hover:bg-accent'
                            )}
                        >
                            <item.icon className="size-4 shrink-0" />
                            <span>{item.label}</span>
                        </Link>
                    ))}
                </div>
            )}
        </div>
    );
}
