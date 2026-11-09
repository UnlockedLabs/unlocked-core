import React, { useState, useEffect } from 'react';
import { User } from '@/types';
import { AuthContext, checkSession, signInAgain } from '@/auth/useAuth';
import { onSessionExpired } from '@/auth/sessionExpiry';
import { useNavigate } from 'react-router-dom';
import { tabSessionManager } from '@/session/tabSession';
import { analyticsDistinctId, identifyUser } from '@/lib/events';
import { setDigitalTranscriptStorageOwner } from '@/types/digital-transcript';

const RECHECK_ON_VISIBLE_MS = 60 * 1000;

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({
    children
}) => {
    const passReset = '/reset-password';
    const [user, setUser] = useState<User | undefined>();
    const [unavailable, setUnavailable] = useState(false);
    const navigate = useNavigate();

    useEffect(() => {
        let cancelled = false;
        const checkAuth = async () => {
            const session = await checkSession();
            if (cancelled) return;
            if (session === 'unavailable') {
                setUnavailable(true);
                return;
            }
            if (
                session === 'signed-out' ||
                !(await tabSessionManager.validateSession())
            ) {
                if (cancelled) return;
                await signInAgain(window.location);
                return;
            }
            if (cancelled) return;
            const authUser = session;
            // Before anything reads Learning Record storage: discard it if
            // it belongs to a different resident. Facility workstations are
            // shared, the keys are per-origin, and logging out clears none of
            // them — so without this the next person to sign in inherits the
            // previous one's unfinished achievement and their sitting.
            await setDigitalTranscriptStorageOwner(
                analyticsDistinctId(authUser.id)
            );
            identifyUser(authUser);
            setUser(authUser);
        };
        void checkAuth();
        return () => {
            cancelled = true;
        };
    }, []);

    // A session can end mid-use (expired, revoked, signed out in another tab).
    // Recheck on any 401 and when the tab comes back into view. Core also sends
    // 401 for "not an admin" and "feature not enabled", so only a check that
    // says signed-out sends the resident to login.
    const signedIn = user !== undefined;
    useEffect(() => {
        if (!signedIn) return;
        let checking = false;
        let lastCheck = Date.now();
        const recheck = () => {
            if (checking) return;
            checking = true;
            lastCheck = Date.now();
            void checkSession().then(async (session) => {
                if (session === 'signed-out') {
                    // Leaves the page, so checking stays set and later 401s
                    // don't start a second logout.
                    await signInAgain(window.location);
                    return;
                }
                checking = false;
            });
        };
        const onVisible = () => {
            if (
                document.visibilityState === 'visible' &&
                Date.now() - lastCheck >= RECHECK_ON_VISIBLE_MS
            ) {
                recheck();
            }
        };
        const unsubscribe = onSessionExpired(recheck);
        document.addEventListener('visibilitychange', onVisible);
        return () => {
            unsubscribe();
            document.removeEventListener('visibilitychange', onVisible);
        };
    }, [signedIn]);

    useEffect(() => {
        if (user?.password_reset && window.location.pathname !== passReset) {
            navigate(passReset);
        }
    }, [user, navigate]);

    if (unavailable) {
        // Hands it to the route's error page, so a resident sees "Something
        // went wrong" and a way back instead of a blank screen.
        throw new Error("Couldn't check the session: the server didn't answer");
    }
    if (!user) {
        return null;
    }

    return (
        <AuthContext.Provider value={{ user, setUser }}>
            {children}
        </AuthContext.Provider>
    );
};
