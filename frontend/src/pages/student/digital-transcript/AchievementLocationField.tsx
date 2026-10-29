import { useState } from 'react';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import type { TranscriptEntry } from '@/types/digital-transcript';
import { learningRecordQuestionHeaderClassName } from './learningRecordButtons';
import { FUNNEL_FIELD_DESCRIPTIONS } from './transcriptReflectionConfig';
import { useLearningRecordFacilities } from './useLearningRecordFacilities';

/** Sentinel for the "Other" item — no facility id can collide with it. */
const OTHER_VALUE = 'other';

/** Shown for a facility already on the entry whose name we couldn't resolve. */
const UNKNOWN_FACILITY_LABEL = 'Saved location (name unavailable)';

/** Keep in step with maxFacilityOtherRunes in backend/src/modules/learningrecord/handlers.go. */
const MAX_FACILITY_OTHER_LENGTH = 120;

export const ACHIEVEMENT_LOCATION_LABEL =
    'Where did this achievement take place?';

interface AchievementLocationFieldProps {
    entry: TranscriptEntry;
    onChange: (patch: Partial<TranscriptEntry>) => void;
}

/**
 * Achievement location — the facility where the program was completed, which is
 * often not the resident's current facility. Mount with `key={entry.id}` so the
 * "Other" toggle resets when a different achievement is opened.
 *
 * Picking "Other" is only a pending choice: it reveals the free-text input but
 * leaves any already-saved facility in place, so an autosave mid-edit can't drop
 * the location. The switch commits once the resident actually types something.
 */
export function AchievementLocationField({
    entry,
    onChange
}: AchievementLocationFieldProps) {
    const { facilities, loaded, failed } = useLearningRecordFacilities();
    const [otherSelected, setOtherSelected] = useState(
        entry.facilityOther.trim() !== ''
    );
    // Free text always implies Other, so a value arriving after mount still wins.
    const showOther = otherSelected || entry.facilityOther.trim() !== '';
    // "Other" chosen but nothing typed yet — no location has been recorded from it.
    const otherIsEmpty = showOther && entry.facilityOther.trim() === '';

    const listedFacility = facilities.find((f) => f.id === entry.facilityId);
    // Best name available for the entry's facility: the cached copy first, then the
    // fetched list, since either source can be missing on its own.
    const savedFacilityName =
        entry.facilityId === null
            ? ''
            : entry.facilityName.trim() || (listedFacility?.name ?? '');

    /**
     * The entry's facility as an option of its own when the fetched list doesn't
     * contain it — the list failed to load, is still loading, or no longer offers
     * that facility. Without it the trigger falls back to the placeholder and the
     * resident can't see the facility the record would still be saved with.
     */
    const missingSelected =
        entry.facilityId !== null && !listedFacility
            ? {
                  id: entry.facilityId,
                  name: savedFacilityName || UNKNOWN_FACILITY_LABEL
              }
            : null;
    // A facility still on the entry is the location that stays saved meanwhile.
    const keptFacility =
        otherIsEmpty && entry.facilityId !== null
            ? savedFacilityName || 'the location already saved'
            : '';

    const selectValue = showOther
        ? OTHER_VALUE
        : entry.facilityId !== null
          ? String(entry.facilityId)
          : '';

    function handleSelect(value: string) {
        if (value === OTHER_VALUE) {
            setOtherSelected(true);
            return;
        }
        const id = Number(value);
        const facility = facilities.find((f) => f.id === id);
        setOtherSelected(false);
        onChange({
            facilityId: id,
            // Re-picking the fallback option must not wipe the cached name.
            facilityName:
                facility?.name ??
                (id === entry.facilityId ? entry.facilityName : ''),
            facilityOther: ''
        });
    }

    /**
     * The free text and a facility id are mutually exclusive, but only clear the
     * facility once there is text to replace it with — blanking the input again
     * would otherwise leave the achievement with no location at all.
     */
    function handleOtherText(value: string) {
        if (value.trim() === '') {
            onChange({ facilityOther: value });
            return;
        }
        onChange({ facilityOther: value, facilityId: null, facilityName: '' });
    }

    const selectId = `ach-location-${entry.id}`;
    const otherId = `ach-location-other-${entry.id}`;

    return (
        <div data-slot="achievement-location">
            <div className={learningRecordQuestionHeaderClassName}>
                <Label
                    htmlFor={selectId}
                    className="text-base font-medium leading-snug text-foreground"
                >
                    {ACHIEVEMENT_LOCATION_LABEL}
                </Label>
                <p className="text-xs leading-relaxed text-muted-foreground">
                    {FUNNEL_FIELD_DESCRIPTIONS.location}
                </p>
            </div>
            <Select value={selectValue} onValueChange={handleSelect}>
                <SelectTrigger
                    id={selectId}
                    data-slot="transcript-location"
                    className="h-10 w-full border-border/80 bg-muted/40"
                >
                    <SelectValue
                        placeholder={
                            loaded ? 'Select a location' : 'Loading locations…'
                        }
                    />
                </SelectTrigger>
                <SelectContent>
                    {missingSelected ? (
                        <SelectItem value={String(missingSelected.id)}>
                            {missingSelected.name}
                        </SelectItem>
                    ) : null}
                    {facilities.map((facility) => (
                        <SelectItem
                            key={facility.id}
                            value={String(facility.id)}
                        >
                            {facility.name}
                        </SelectItem>
                    ))}
                    <SelectItem value={OTHER_VALUE}>
                        Other (not listed)
                    </SelectItem>
                </SelectContent>
            </Select>
            {failed ? (
                <p className="mt-2 text-sm text-destructive" role="alert">
                    {missingSelected
                        ? `Couldn't load the list of locations. This achievement will be saved with "${missingSelected.name}" — choose "Other (not listed)" to enter a different one.`
                        : `Couldn't load the list of locations. You can still enter one under "Other (not listed)".`}
                </p>
            ) : null}
            {showOther ? (
                <div className="mt-3">
                    <Label
                        htmlFor={otherId}
                        className="text-sm font-normal leading-snug text-foreground"
                    >
                        Where was it? Add the city or state if that helps.
                    </Label>
                    <Input
                        id={otherId}
                        data-slot="transcript-location-other"
                        value={entry.facilityOther}
                        onChange={(e) => handleOtherText(e.target.value)}
                        maxLength={MAX_FACILITY_OTHER_LENGTH}
                        placeholder="Facility, program, or place"
                        className="mt-1.5 h-10 border-border/80 bg-muted/40"
                    />
                    {otherIsEmpty ? (
                        <p
                            className="mt-1.5 text-xs leading-relaxed text-muted-foreground"
                            role="status"
                        >
                            {keptFacility
                                ? `Type a location to use it — until then this achievement stays at ${keptFacility}.`
                                : 'Type a location, or this achievement will be saved without one.'}
                        </p>
                    ) : null}
                </div>
            ) : null}
        </div>
    );
}
