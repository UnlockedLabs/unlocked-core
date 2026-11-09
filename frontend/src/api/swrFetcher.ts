import { ANALYTICS_EVENTS, analyticsUrl, captureEvent } from '@/lib/events';
import { reportUnauthorized } from '@/auth/sessionExpiry';

/**
 * Global SWR fetcher.
 *
 * Lives here rather than inline in `main.tsx` so failures can report to the same
 * `api_error` event the `API.*` wrapper uses. SWR powers most of the app's
 * page-load GETs, so before this every one of those failures was invisible to
 * analytics — "error states hit during common flows" was missing the most common
 * flow of all, loading a page.
 *
 * Errors are captured and then rethrown unchanged, so SWR's own error handling
 * behaves exactly as it did.
 */
export const swrFetcher = async (url: string): Promise<unknown> => {
    let res: Response;
    try {
        res = await fetch(url, {
            credentials: 'include',
            headers: {
                'X-Requested-With': 'XMLHttpRequest',
                Accept: 'application/json',
                'Content-Type': 'application/json'
            }
        });
    } catch (err) {
        // Network-level failure: no HTTP status exists, so report 0 to match the
        // convention in api.ts.
        captureEvent(ANALYTICS_EVENTS.ApiError, {
            status: 0,
            method: 'GET',
            ...analyticsUrl(url),
            message: err instanceof Error ? err.message : 'Network error'
        });
        throw err;
    }
    if (!res.ok) {
        if (res.status === 401) reportUnauthorized();
        captureEvent(ANALYTICS_EVENTS.ApiError, {
            status: res.status,
            method: 'GET',
            ...analyticsUrl(url),
            message: res.statusText || `HTTP ${res.status}`
        });
        throw new Error(res.statusText);
    }
    try {
        return (await res.json()) as unknown;
    } catch (err) {
        // An OK response with an empty or malformed body still fails the page.
        // Without this the request looks successful to analytics while SWR
        // surfaces an error to the user.
        captureEvent(ANALYTICS_EVENTS.ApiError, {
            status: res.status,
            method: 'GET',
            ...analyticsUrl(url),
            message: err instanceof Error ? err.message : 'Invalid JSON'
        });
        throw err;
    }
};
