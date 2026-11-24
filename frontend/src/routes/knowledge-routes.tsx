import { declareAuthenticatedRoutes } from '@/auth/declareAuthenticatedRoutes';
import { AdminRoles, AllRoles } from '@/auth/useAuth';
import { FeatureAccess } from '@/types';
import Error from '@/pages/Error';
import type { RouteObject } from 'react-router-dom';

export const KnowledgeCenterAdminRoutes: RouteObject =
    declareAuthenticatedRoutes(
        [
            {
                path: 'knowledge-center-management',
                lazy: () =>
                    import('@/features/open-content/admin').then((m) => ({
                        Component: m.KnowledgeCenterManagement
                    })),
                handle: { title: 'Knowledge Center' }
            }
        ],
        AdminRoles,
        [FeatureAccess.OpenContentAccess]
    );

export const KnowledgeCenterRoutes: RouteObject = declareAuthenticatedRoutes(
    [
        {
            path: 'knowledge-center',
            lazy: () =>
                import('@/features/open-content/resident').then((m) => ({
                    Component: m.ResidentKnowledgeCenter
                })),
            handle: { title: 'Knowledge Center' }
        },
        {
            path: 'knowledge-center/search',
            lazy: () =>
                import('@/features/open-content/resident').then((m) => ({
                    Component: m.KnowledgeCenterSearch
                })),
            handle: { title: 'Knowledge Center Search' }
        },
        {
            path: 'viewer/libraries/:id',
            lazy: () =>
                import('@/features/open-content/resident').then((m) => ({
                    Component: m.LibraryViewer
                })),
            errorElement: <Error />
        },
        {
            path: 'viewer/videos/:id',
            lazy: () =>
                import('@/features/open-content/resident').then((m) => ({
                    Component: m.VideoViewer
                })),
            errorElement: <Error />
        }
    ],
    AllRoles,
    [FeatureAccess.OpenContentAccess]
);
