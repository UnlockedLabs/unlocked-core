import React, { useState, useEffect } from 'react';
import { User } from '@/types';
import { AuthContext, checkSession, signInAgain } from '@/auth/useAuth';
import { useNavigate } from 'react-router-dom';
import { tabSessionManager } from '@/session/tabSession';
import { analyticsDistinctId, identifyUser } from '@/lib/events';
import { setDigitalTranscriptStorageOwner } from '@/types/digital-transcript';

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
