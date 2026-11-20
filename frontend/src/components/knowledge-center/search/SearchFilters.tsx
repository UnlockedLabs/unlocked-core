import { useState } from 'react';
import useSWR from 'swr';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import {
    TypeOption,
    LIBRARIES_LIST_KEY,
    checkedTypeIds,
    typesForChecked
} from './searchFilterOptions';
import { Library, Option, ServerResponseMany } from '@/types';

const VISIBLE_LIBRARIES = 5;

export default function SearchFilters({
    options,
    types,
    libraryIds,
    tag,
    onTypesChange,
    onLibraryIdsChange,
    onTagChange
}: {
    options: TypeOption[];
    /** The URL's `types` values. */
    types: string[];
    libraryIds: string[];
    tag: string;
    onTypesChange: (types: string[]) => void;
    onLibraryIdsChange: (ids: string[]) => void;
    onTagChange: (tag: string) => void;
}) {
    const [showAllLibraries, setShowAllLibraries] = useState(false);
    const { data: libData } =
        useSWR<ServerResponseMany<Library>>(LIBRARIES_LIST_KEY);
    const { data: tagsData } = useSWR<ServerResponseMany<Option>>('/api/tags');
    const libraries = libData?.data ?? [];
    const categories = tagsData?.data ?? [];
    const shownLibraries = showAllLibraries
        ? libraries
        : libraries.slice(0, VISIBLE_LIBRARIES);

    const checked = checkedTypeIds(options, types);

    const toggleType = (id: string, on: boolean) => {
        const next = on ? [...checked, id] : checked.filter((c) => c !== id);
        if (next.length === 0) return; // at least one kind stays selected
        onTypesChange(typesForChecked(options, next));
    };

    const toggleLibrary = (id: string, on: boolean) =>
        onLibraryIdsChange(
            on ? [...libraryIds, id] : libraryIds.filter((l) => l !== id)
        );

    return (
        <aside className="space-y-6" aria-label="Search filters">
            <fieldset>
                <legend className="text-sm font-semibold text-gray-900 mb-2">
                    Content type
                </legend>
                <div className="space-y-2">
                    {options.map((o) => (
                        <div key={o.id} className="flex items-center gap-2">
                            <Checkbox
                                id={`kc-type-${o.id}`}
                                checked={checked.includes(o.id)}
                                disabled={
                                    checked.length === 1 &&
                                    checked.includes(o.id)
                                }
                                onCheckedChange={(v) =>
                                    toggleType(o.id, v === true)
                                }
                            />
                            <Label htmlFor={`kc-type-${o.id}`}>{o.label}</Label>
                        </div>
                    ))}
                </div>
            </fieldset>

            <fieldset>
                <legend className="text-sm font-semibold text-gray-900 mb-2">
                    Library
                </legend>
                <div className="space-y-2">
                    {shownLibraries.map((lib) => (
                        <div key={lib.id} className="flex items-center gap-2">
                            <Checkbox
                                id={`kc-lib-${lib.id}`}
                                checked={libraryIds.includes(String(lib.id))}
                                onCheckedChange={(v) =>
                                    toggleLibrary(String(lib.id), v === true)
                                }
                            />
                            <Label htmlFor={`kc-lib-${lib.id}`}>
                                {lib.title}
                            </Label>
                        </div>
                    ))}
                </div>
                {libraries.length > VISIBLE_LIBRARIES && (
                    <button
                        type="button"
                        className="mt-2 text-sm text-brand hover:underline"
                        onClick={() => setShowAllLibraries((s) => !s)}
                    >
                        {showAllLibraries
                            ? 'Show fewer'
                            : `Show all (${libraries.length})`}
                    </button>
                )}
            </fieldset>

            <div>
                <Label
                    htmlFor="kc-category"
                    className="text-sm font-semibold text-gray-900 mb-2"
                >
                    Category
                </Label>
                <Select
                    value={tag || 'all'}
                    onValueChange={(v) => onTagChange(v === 'all' ? '' : v)}
                >
                    <SelectTrigger id="kc-category" className="w-full">
                        <SelectValue placeholder="All categories" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">All categories</SelectItem>
                        {categories.map((cat) => (
                            <SelectItem
                                key={String(cat.key)}
                                value={String(cat.key)}
                            >
                                {String(cat.value)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>
        </aside>
    );
}
