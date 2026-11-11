import { useEffect } from 'react';
import { Navigate, Outlet } from 'react-router-dom';
import { toast } from 'sonner';
import { AUTHCALLBACK, hasFeature, useAuth } from '@/auth/useAuth';
import { FeatureAccess, User, UserRole } from '@/types';

const NO_ACCESS = "You don't have access to that page";

function canOpen(
    user: User,
    allowedRoles?: UserRole[],
    features?: FeatureAccess[]
): boolean {
    if (allowedRoles && !allowedRoles.includes(user.role)) {
        return false;
    }
    return !features || hasFeature(user, ...features);
}

export default function RouteGuard({
    allowedRoles,
    features
}: {
    allowedRoles?: UserRole[];
    features?: FeatureAccess[];
}) {
    const { user } = useAuth();
    const denied = user !== undefined && !canOpen(user, allowedRoles, features);

    useEffect(() => {
        if (denied) {
            // The id keeps a repeat (StrictMode, a second guard) to one toast.
            toast.error(NO_ACCESS, { id: NO_ACCESS });
        }
    }, [denied]);

    // AuthProvider (mounted in AuthenticatedShell) guarantees a user before
    // children render, but guard defensively.
    if (!user) {
        return null;
    }
    if (denied) {
        return <Navigate to={AUTHCALLBACK} replace />;
    }
    return <Outlet />;
}
