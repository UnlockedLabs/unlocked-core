import { json, LoaderFunction, redirect } from 'react-router-dom';
import {
    OpenContentItem,
    ServerResponse,
    Library,
    UserCoursesInfo,
    ActivityMapData,
    UserRole,
    Option,
    ProviderPlatform,
    Program,
    RouteTitleHandler,
    Cohort,
    ServerResponseOne,
    ClassLoaderData,
    ProgramOverview,
    Room,
    BreadcrumbItem
} from '@/types';
import API from '@/api/api';
import { fetchUser, hasFeature } from '@/auth/useAuth';
import { FeatureAccess } from '@/types';
import { decodeHtmlEntities } from '@/lib/decodeHtmlEntities';

function buildClassBreadcrumbs(
    cls: Cohort,
    currentTab: string
): BreadcrumbItem[] {
    return [
        { label: 'Dashboard', href: '/dashboard' },
        { label: 'Programs', href: '/programs' },
        {
            label: cls.program.name,
            href: `/programs/${cls.program.id}`
        },
        {
            label: cls.class_name,
            href: `/program-classes/${cls.id}/detail`
        },
        { label: currentTab }
    ];
}

// Feeds /home (ResidentHome) only. That page reads just topUserContent and
// topFacilityContent from here — its featured / favorites / helpful-links panels
// come from their own feature-gated SWR hooks, and nothing reads libraryOptions.
// The four extra requests this used to make were fetched, serialized, and
// discarded on every navigation, and each 401'd once Knowledge Center was turned
// off for the facility.
export const getStudentLevel1Data: LoaderFunction = async () => {
    const user = await fetchUser();
    const empty = { topUserContent: [], topFacilityContent: [] };
    // Must always resolve to a shaped object: ResidentHome destructures this
    // loader's data directly, so a bare `undefined` throws during render and the
    // resident gets the generic error page instead of the homepage.
    if (!user) return json(empty);

    if (!hasFeature(user, FeatureAccess.OpenContentAccess)) {
        return json(empty);
    }

    const [userContentResp, facilityContentResp] = await Promise.all([
        API.get(`open-content/activity/${user.id}`),
        API.get(`open-content/activity`)
    ]);

    return json({
        topUserContent: userContentResp.success
            ? (userContentResp.data as OpenContentItem[])
            : [],
        topFacilityContent: facilityContentResp.success
            ? (facilityContentResp.data as OpenContentItem[])
            : []
    });
};

export const getAdminLevel1Data: LoaderFunction = async () => {
    const [featuredResp] = await Promise.all([
        API.get(`libraries?visibility=featured&order_by=created_at`)
    ]);

    const featured = featuredResp.success
        ? (featuredResp.data as Library[])
        : [];

    return json({
        featured: featured
    });
};

export const getStudentLayer2Data: LoaderFunction = async () => {
    const user = await fetchUser();
    if (!user) return;
    const endDate = new Date();
    const startDate = new Date();
    startDate.setDate(endDate.getDate() - 7);
    const [coursesResp, activityResp] = await Promise.all([
        API.get(`users/${user.id}/courses?order=desc&order_by=recent_activity`),
        API.get(
            `users/${user.id}/daily-activity?start_date=${startDate.toISOString()}&end_date=${endDate.toISOString()}`
        )
    ]);

    const courses = coursesResp.data as UserCoursesInfo;
    const activity = activityResp.data as { activities: ActivityMapData[] };

    return json({
        courses: courses.courses,
        week_activity: activity.activities || []
    });
};

export const getLibraryLayoutData: LoaderFunction = async ({
    request
}: {
    request: Request;
}) => {
    const [tagsResp] = await Promise.all([API.get(`tags`)]);
    const categories = tagsResp.data as Option[];
    const libraryOptions = await getLibraryOptionsHelper({ request });
    return json({ categories: categories, libraryOptions: libraryOptions });
};

const getLibraryOptionsHelper = async ({ request }: { request: Request }) => {
    const user = await fetchUser();
    if (!user) return;
    const visibilityParam =
        user.Role != UserRole.Student &&
        (request.url.includes('management') || request.url.includes('viewer'))
            ? '&visibility=all'
            : '&visibility=visible';
    const libraryResp = await API.get(
        `libraries?all=true&order_by=title&order=asc${visibilityParam}`
    );
    const libraryOptions = libraryResp.success
        ? (libraryResp.data as Library[]).map((library) => ({
              key: library.id,
              value: decodeHtmlEntities(library.title)
          }))
        : [];
    return libraryOptions;
};

export const getProgramData: LoaderFunction = async ({ params }) => {
    const { program_id } = params;
    const [tagsResp] = await Promise.all([API.get(`tags`)]);
    const categories = tagsResp.data as Option[];
    let program: ProgramOverview | undefined;
    let redirectUrl: string | undefined;
    let breadcrumbs: BreadcrumbItem[] | undefined;

    if (program_id) {
        const resp = await API.get(`programs/${program_id}`);
        if (resp.success) {
            program = resp.data as ProgramOverview;
            breadcrumbs = [
                { label: 'Dashboard', href: '/dashboard' },
                { label: 'Programs', href: '/programs' },
                { label: program.name }
            ];
        } else {
            return redirectOnError(resp);
        }
    }
    return json({
        categories: categories,
        program: program,
        redirect: redirectUrl,
        breadcrumbs
    });
};

export const getProviderPlatforms: LoaderFunction = async () => {
    const response: ServerResponse<ProviderPlatform> =
        await API.get<ProviderPlatform>(`provider-platforms?only=oidc_enabled`);
    if (response.success) {
        return json({ providerPlatforms: response.data as ProviderPlatform[] });
    }
    return json({ providerPlatforms: [] });
};

export const getProgramTitle: LoaderFunction = async ({
    params,
    request
}): Promise<ClassLoaderData | Response> => {
    const { id, class_id } = params;
    let cls: Cohort | undefined;
    let programName = 'Class Details';
    let rooms: Room[] = [];
    let breadcrumbs: BreadcrumbItem[] | undefined;

    if (id) {
        const resp = await API.get(`programs/${id}`);
        if (resp.success) {
            programName = 'Program: ' + (resp.data as Program).name;
        } else {
            return redirectOnError(resp);
        }
    }
    if (class_id && class_id != 'new') {
        const classResp = (await API.get(
            `program-classes/${class_id}`
        )) as ServerResponseOne<Cohort>;
        if (classResp.success) {
            cls = classResp.data;
        } else {
            return redirectOnError(classResp);
        }
    }

    const roomsUrl = cls ? `rooms?facility_id=${cls.facility_id}` : 'rooms';
    const roomsResp = await API.get(roomsUrl);
    if (roomsResp.success) {
        rooms = roomsResp.data as Room[];
    }

    const url = new URL(request.url);
    const isAddEnrollment = url.pathname.includes('enrollments/add') && cls;

    if (isAddEnrollment && cls) {
        breadcrumbs = [
            { label: 'Dashboard', href: '/dashboard' },
            { label: 'Programs', href: '/programs' },
            { label: cls.program.name, href: `/programs/${cls.program.id}` },
            {
                label: cls.class_name,
                href: `/program-classes/${cls.id}/detail`
            },
            {
                label: 'Enrollment',
                href: `/program-classes/${cls.id}/enrollments`
            },
            { label: 'Add Resident' }
        ];
    }

    return {
        title: programName,
        class: cls,
        rooms: rooms,
        breadcrumbs
    };
};

const tabNameMap: Record<string, string> = {
    enrollments: 'Enrollment',
    attendance: 'Attendance',
    schedule: 'Schedule'
};

export const getClassTitle: LoaderFunction = async ({
    params,
    request
}): Promise<ClassLoaderData | Response> => {
    const { class_id, date } = params;
    const classResp = (await API.get(
        `program-classes/${class_id}`
    )) as ServerResponseOne<Cohort>;
    if (classResp.success) {
        const cls = classResp.data;
        const url = new URL(request.url);
        const pathParts = url.pathname.split('/');
        const isEventAttendance =
            pathParts.includes('events') && pathParts.includes('attendance');

        let breadcrumbs: BreadcrumbItem[];
        if (isEventAttendance && date) {
            breadcrumbs = [
                { label: 'Dashboard', href: '/dashboard' },
                { label: 'Programs', href: '/programs' },
                {
                    label: cls.program.name,
                    href: `/programs/${cls.program.id}`
                },
                {
                    label: cls.class_name,
                    href: `/program-classes/${cls.id}/detail`
                },
                { label: 'Take Attendance' }
            ];
        } else {
            const tabSegment = pathParts[pathParts.length - 1];
            const tabName = tabNameMap[tabSegment] ?? tabSegment;
            breadcrumbs = buildClassBreadcrumbs(cls, tabName);
        }

        return {
            title: cls.class_name,
            class: cls,
            breadcrumbs
        };
    } else {
        return redirectOnError(classResp);
    }
};

export const getProgram: LoaderFunction = async ({ params }) => {
    const resp = (await API.get(`programs/${params.id}`)) as ServerResponseOne<
        ProgramOverview | Response
    >;
    if (!resp.success) {
        return redirectOnError(resp);
    }
    return json(resp.data);
};

export function resolveTitle<T>(
    handle: RouteTitleHandler<T> | undefined,
    data: T
): string {
    if (!handle) return '';
    return typeof handle.title === 'function'
        ? handle.title(data)
        : handle.title;
}

function redirectOnError<T>(resp: ServerResponse<T>) {
    if (resp.status === 401 || resp.status === 403) {
        return redirect('/unauthorized');
    }
    if (resp.status === 404) {
        return redirect('/404');
    }
    return redirect('/error');
}

export const getFilterDropdowns: LoaderFunction = async () => {
    const resp = await API.get(`programs/filters`);
    return json(resp.data);
};
