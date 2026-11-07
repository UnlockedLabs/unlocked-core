import { Dispatch, SetStateAction, createContext, useContext } from 'react';
import { LoaderFunction, json, redirect } from 'react-router-dom';
import {
    AuthFlow,
    AuthResponse,
    INIT_KRATOS_LOGIN_FLOW,
    OryFlow,
    OrySessionWhoami,
    ServerResponseOne,
    User,
    UserRole,
    FeatureAccess,
    Facility
} from '@/types';
import API from '@/api/api';
import { tabSessionManager } from '@/session/tabSession';
import { resetAnalytics } from '@/lib/events';
import {
    getSessionItem,
    isSessionStorageAvailable,
    removeSessionItem,
    setSessionItem
} from '@/lib/safeSessionStorage';

interface AuthContextType {
    user: User | undefined;
    setUser: Dispatch<SetStateAction<User | undefined>>;
}

export const AuthContext = createContext<AuthContextType | undefined>(
    undefined
);

export const AdminRoles = [
    UserRole.SystemAdmin,
    UserRole.FacilityAdmin,
    UserRole.DepartmentAdmin
];

export const AllRoles = [UserRole.Student, ...AdminRoles];

export function canSwitchFacility(user: User): boolean {
    return isSysAdmin(user) || isDeptAdmin(user);
}

export const isAdministrator = (user: User | undefined): boolean => {
    return user !== undefined && AdminRoles.includes(user.role);
};

export const isSysAdmin = (u: User) => u.role === UserRole.SystemAdmin;

export const isDeptAdmin = (u: User) => u.role === UserRole.DepartmentAdmin;

export const isFacilityAdmin = (user: User | undefined): boolean => {
    return user?.role === UserRole.FacilityAdmin;
};

export const isStudent = (user: User): boolean => {
    return !AdminRoles.includes(user.role);
};

export const SESSION_URL = '/sessions/whoami';
export const KRATOS_CHECK_FLOW_URL = '/self-service/login/flows?id=';
export const AUTHCALLBACK = '/authcallback';
const KRATOS_LOGOUT_URL = '/self-service/logout/browser';
const RETURN_TO_KEY = 'return_to';
// Kratos's login flow lifespan. A saved page older than this was left by
// someone who walked away from login, so the next person to sign in in that
// tab doesn't inherit it.
const RETURN_TO_MAX_AGE_MS = 10 * 60 * 1000;

/**
 * Who core says is signed in: the user, 'signed-out' when it rejects the
 * session, or 'unavailable' when it couldn't answer (server down, network).
 */
export async function checkSession(): Promise<
    User | 'signed-out' | 'unavailable'
> {
    const response = await API.get<User>('auth');
    if (response.success) {
        return response.data as User;
    }
    return response.status === 401 || response.status === 403
        ? 'signed-out'
        : 'unavailable';
}

export async function fetchUser(): Promise<User | undefined> {
    const session = await checkSession();
    return typeof session === 'string' ? undefined : session;
}

type Page = Pick<URL, 'pathname' | 'search' | 'hash'>;

/**
 * Sends a signed-out visitor to login, and once they sign in, back to page.
 * The page is remembered per tab, for RETURN_TO_MAX_AGE_MS, and the next
 * sign-in in that tab uses it up.
 */
export async function signInAgain({
    pathname,
    search,
    hash
}: Page): Promise<void> {
    if (pathname !== AUTHCALLBACK) {
        setSessionItem(
            RETURN_TO_KEY,
            JSON.stringify({ path: pathname + search + hash, at: Date.now() })
        );
    }
    await handleLogout();
}

function takeReturnTo(): string | null {
    const saved = getSessionItem(RETURN_TO_KEY);
    removeSessionItem(RETURN_TO_KEY);
    try {
        const { path, at } = JSON.parse(saved ?? '') as {
            path?: unknown;
            at?: unknown;
        };
        const fresh =
            typeof at === 'number' && Date.now() - at < RETURN_TO_MAX_AGE_MS;
        // Only a path on this site: browsers read "//host" and "/\host" as
        // another site.
        return fresh &&
            typeof path === 'string' &&
            path.startsWith('/') &&
            !/^\/[/\\]/.test(path)
            ? path
            : null;
    } catch {
        return null;
    }
}

export const checkRole: LoaderFunction = async () => {
    const user = await fetchUser();
    const returnTo = user ? takeReturnTo() : null;
    return redirect(returnTo ?? getDashboardLink(user));
};

export function useAuth(): AuthContextType {
    const context = useContext(AuthContext);
    if (!context) {
        throw new Error('useAuth must be used within an AuthProvider');
    }
    return context;
}

export const initFlow = async (flow: string): Promise<AuthFlow> => {
    try {
        const resp = await fetch(KRATOS_CHECK_FLOW_URL + flow, {
            credentials: 'include'
        });
        if (resp.status !== 200) {
            return renewLoginFlow();
        }
        const jsonResp = (await resp.json()) as OryFlow;
        const flowExpired = getSessionItem(FLOW_EXPIRED_KEY) !== null;
        removeSessionItem(FLOW_EXPIRED_KEY);
        removeSessionItem(FLOW_RETRIES_KEY);
        return {
            flow_id: jsonResp.id,
            challenge: jsonResp.oauth2_login_challenge,
            csrf_token: jsonResp.ui.nodes[0].attributes.value,
            flow_expired: flowExpired
        };
    } catch {
        return renewLoginFlow();
    }
};

const FLOW_EXPIRED_KEY = 'login_flow_expired';
const FLOW_RETRIES_KEY = 'login_flow_retries';
const MAX_FLOW_RETRIES = 3;

/**
 * Replaces a stale login flow (expired, unknown, or from an old bookmark) by
 * sending the browser back to Kratos for a fresh one. The retry count lives in
 * sessionStorage so it survives the redirect; once it runs out we stop and let
 * the user retry by hand instead of bouncing between Kratos and /login forever
 * (EN-118).
 */
export function renewLoginFlow(challenge?: string): AuthFlow {
    const retries = Number(getSessionItem(FLOW_RETRIES_KEY) ?? 0);
    if (retries >= MAX_FLOW_RETRIES) {
        return { flow_id: '', csrf_token: '', flow_error: true };
    }
    setSessionItem(FLOW_RETRIES_KEY, String(retries + 1));
    setSessionItem(FLOW_EXPIRED_KEY, '1');
    const url = challenge
        ? `${INIT_KRATOS_LOGIN_FLOW}?login_challenge=${encodeURIComponent(challenge)}`
        : INIT_KRATOS_LOGIN_FLOW;
    return redirectTo(url);
}

export function retryLoginFlow(): void {
    removeSessionItem(FLOW_RETRIES_KEY);
    window.location.href = INIT_KRATOS_LOGIN_FLOW;
}

export const hasFeature = (user: User, ...axx: FeatureAccess[]): boolean => {
    return axx.every((ax) => user.feature_access?.includes(ax) ?? false);
};

export const checkDefaultFacility: LoaderFunction = async () => {
    const resp = await API.get<Facility>('facilities/1');
    if (resp.success && resp.type == 'one') {
        return json<Facility>(resp.data);
    } else {
        return json<null>(null);
    }
};

const redirectTo = (url: string): AuthFlow => {
    return {
        flow_id: '',
        challenge: '',
        csrf_token: '',
        redirect_to: url
    };
};

export const checkExistingFlow: LoaderFunction = async ({ request }) => {
    // This runs before anything that can redirect. Without sessionStorage the
    // tab-session flag can never be set, so a login here could never stick.
    // Browsers that block site data usually block Kratos's cookies too, and
    // then initFlow() can't read the flow and sends us back to
    // INIT_KRATOS_LOGIN_FLOW, which lands here again. Stopping on /login with
    // an explanation avoids both the silent lockout and the loop (EN-80).
    if (!isSessionStorageAvailable()) {
        return json<AuthFlow>({
            flow_id: '',
            csrf_token: '',
            storage_blocked: true
        });
    }
    const url = new URL(request.url);
    const queryParams = new URLSearchParams(url.search);
    const flow = queryParams.get('flow');
    if (!flow) {
        return json<AuthFlow>(redirectTo(INIT_KRATOS_LOGIN_FLOW));
    }
    const attributes = await initFlow(flow);
    if (attributes.redirect_to || attributes.flow_error) {
        return json<AuthFlow>(attributes);
    }
    if (!tabSessionManager.hasLocalSession()) {
        return json<AuthFlow>({
            flow_id: attributes.flow_id,
            challenge: attributes.challenge,
            csrf_token: attributes.csrf_token,
            flow_expired: attributes.flow_expired
        });
    }
    try {
        const checkResp = await fetch(SESSION_URL, {
            credentials: 'include'
        });
        if (checkResp.status === 200) {
            const jsonResp = (await checkResp.json()) as OrySessionWhoami;
            if (jsonResp.active && jsonResp.identity.traits) {
                if (!attributes.challenge) {
                    return json<AuthFlow>(redirectTo(AUTHCALLBACK));
                }
                const reqBody = {
                    username: jsonResp.identity.traits.username,
                    identity: jsonResp.identity.id,
                    csrf_token: attributes?.csrf_token,
                    session: jsonResp,
                    challenge: attributes?.challenge
                };
                const resp = (await API.post(
                    'auth/refresh',
                    reqBody
                )) as ServerResponseOne<AuthResponse>;
                if (resp.success)
                    return json<AuthFlow>(redirectTo(resp.data.redirect_to));
            }
        }
    } catch {
        // no active sessions found
    }
    return json<AuthFlow>(attributes);
};

/**
 * Ends the Kratos session and lands on login, whatever fails along the way.
 * It asks Kratos directly rather than core, because it also runs after core
 * has rejected the session. Ending the Kratos session matters then too: a
 * session Kratos still accepts (a deactivated user's) would send the login
 * flow straight back into the app, and the app back to login.
 */
export async function handleLogout(): Promise<void> {
    resetAnalytics();
    tabSessionManager.onLogout();
    window.dispatchEvent(new Event('logoutEvent'));
    try {
        // Without the Accept header, Kratos answers "no session" by redirecting
        // to its error page instead of a 401.
        const resp = await fetch(KRATOS_LOGOUT_URL, {
            credentials: 'include',
            headers: { Accept: 'application/json' }
        });
        if (resp.ok) {
            const { logout_url } = (await resp.json()) as AuthResponse;
            if (logout_url) {
                window.location.replace(logout_url);
                return;
            }
        }
    } catch {
        // Falls through to the login flow.
    }
    window.location.replace(INIT_KRATOS_LOGIN_FLOW);
}

export const getDashboardLink = (user?: User) => {
    if (!user) return '/';
    return isAdministrator(user) ? getAdminLink() : getResidentLink(user);
};

const getAdminLink = (): string => {
    return '/dashboard';
};

const getResidentLink = (user: User): string => {
    const features = user.feature_access ?? [];
    if (
        features.includes(FeatureAccess.OpenContentAccess) ||
        features.includes(FeatureAccess.LearningRecordAccess)
    ) {
        return '/home';
    }
    if (
        features.includes(FeatureAccess.ProgramAccess) &&
        features.includes(FeatureAccess.ResidentProgramsAccess)
    ) {
        return '/resident-programs';
    }
    return '/temp-home';
};

export function isUserDeactivated(user: User): boolean {
    return user.deactivated_at != null;
}
