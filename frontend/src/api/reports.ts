import { ReportGenerateRequest } from '@/types/reports';
import { reportUnauthorized } from '@/auth/sessionExpiry';

export async function generateReport(
    params: ReportGenerateRequest
): Promise<{ blob: Blob; filename: string }> {
    const csrfMatch = /csrf_token=([^;]+)/.exec(document.cookie);
    const csrfToken = csrfMatch ? csrfMatch[1] : '';

    const resp = await fetch('/api/reports/generate', {
        method: 'POST',
        credentials: 'include',
        headers: {
            'X-Requested-With': 'XMLHttpRequest',
            'Content-Type': 'application/json',
            'X-CSRF-Token': csrfToken
        },
        body: JSON.stringify(params)
    });

    if (!resp.ok) {
        if (resp.status === 401) reportUnauthorized();
        const errorText = await resp.text();
        let errorMessage = 'Failed to generate report';
        try {
            const errorJson = JSON.parse(errorText) as { message?: string };
            errorMessage = errorJson.message ?? errorMessage;
        } catch {
            errorMessage = errorText || errorMessage;
        }
        throw new Error(errorMessage);
    }

    const blob = await resp.blob();
    const disposition = resp.headers.get('Content-Disposition') ?? '';
    const match = /filename[^;=\n]*=((['"]).*?\2|[^;\n]*)/.exec(disposition);
    const filename =
        match?.[1]?.replace(/['"]/g, '') ?? `report-${Date.now()}.csv`;

    return { blob, filename };
}
