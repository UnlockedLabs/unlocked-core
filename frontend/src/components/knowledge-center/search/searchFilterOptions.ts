/**
 * Content-type filter options. `values` are the backend `types` values each
 * checkbox stands for; "Inside libraries" covers library title matches and
 * matches inside library pages. Change the mapping here.
 */
export interface TypeOption {
    id: 'inside' | 'video' | 'link';
    label: string;
    values: string[];
}

export function typeOptions(
    canViewVideos: boolean,
    canViewHelpfulLinks: boolean
): TypeOption[] {
    const options: TypeOption[] = [
        {
            id: 'inside',
            label: 'Inside libraries',
            values: ['library', 'content']
        }
    ];
    if (canViewVideos) {
        options.push({ id: 'video', label: 'Videos', values: ['video'] });
    }
    if (canViewHelpfulLinks) {
        options.push({ id: 'link', label: 'Helpful links', values: ['link'] });
    }
    return options;
}

/** Which checkboxes are checked for the URL's `types` (none in URL = all). */
export function checkedTypeIds(
    options: TypeOption[],
    urlTypes: string[]
): string[] {
    if (urlTypes.length === 0) return options.map((o) => o.id);
    const checked = options
        .filter((o) => o.values.some((v) => urlTypes.includes(v)))
        .map((o) => o.id);
    return checked.length > 0 ? checked : options.map((o) => o.id);
}

/** Backend `types` for a set of checked ids ([] = everything the user can see). */
export function typesForChecked(
    options: TypeOption[],
    checkedIds: string[]
): string[] {
    if (checkedIds.length === options.length) return [];
    return options
        .filter((o) => checkedIds.includes(o.id))
        .flatMap((o) => o.values);
}

/** SWR key for the visible-libraries list used by the filter and the results page. */
export const LIBRARIES_LIST_KEY =
    '/api/libraries?visibility=visible&per_page=500&order_by=title&order=asc';
