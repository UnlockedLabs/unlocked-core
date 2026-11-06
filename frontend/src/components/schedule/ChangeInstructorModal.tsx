import { useState, useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { toast } from 'sonner';
import API from '@/api/api';
import { FacilityProgramClassEvent, Instructor, ChangeReason } from '@/types';
import {
    changeInstructorSchema,
    ChangeInstructorInput
} from '@/lib/validation';
import { FormModal } from '@/components/shared/FormModal';
import { Button } from '@/components/ui/button';
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage
} from '@/components/ui/form';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue
} from '@/components/ui/select';
import { useChangeEventField } from './useChangeEventField';

interface ChangeInstructorModalProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    event: FacilityProgramClassEvent;
    facilityId: string;
    onSuccess: () => void;
}

export function ChangeInstructorModal({
    open,
    onOpenChange,
    event,
    facilityId,
    onSuccess
}: ChangeInstructorModalProps) {
    const form = useForm<ChangeInstructorInput>({
        resolver: zodResolver(changeInstructorSchema),
        defaultValues: {
            instructor_id: '',
            reason: '',
            applyToFuture: false
        }
    });
    const [submitting, setSubmitting] = useState(false);
    const [instructors, setInstructors] = useState<Instructor[]>([]);

    const instructorId = form.watch('instructor_id');
    const reason = form.watch('reason');
    const applyToFuture = form.watch('applyToFuture');

    const { submitSingleSessionChange, submitSeriesChange } =
        useChangeEventField(
            event,
            { instructor_id: instructorId ? Number(instructorId) : null },
            reason ?? ''
        );

    useEffect(() => {
        async function fetchInstructors() {
            const resp = await API.get<Instructor>(
                `program-facilities/${facilityId}/instructors`
            );
            if (resp.success && resp.type === 'many') {
                setInstructors(resp.data.filter((i) => i.id !== 0));
            }
        }
        void fetchInstructors();
    }, [facilityId]);

    useEffect(() => {
        if (open) {
            form.reset({
                instructor_id: '',
                reason: '',
                applyToFuture: false
            });
        }
    }, [open, form]);

    const selectedInstructor = instructors.find(
        (i) => String(i.id) === instructorId
    );
    const selectedInstructorName = selectedInstructor
        ? `${selectedInstructor.name_first} ${selectedInstructor.name_last}`
        : instructorId;

    async function handleSubmit() {
        setSubmitting(true);

        const result = applyToFuture
            ? await submitSeriesChange()
            : await submitSingleSessionChange();

        setSubmitting(false);

        if (result.success) {
            toast.success(
                applyToFuture
                    ? `Instructor changed to ${selectedInstructorName} for all future sessions`
                    : `Instructor changed to ${selectedInstructorName}`
            );
            onOpenChange(false);
            onSuccess();
        } else {
            toast.error(result.message ?? 'Failed to update instructor');
        }
    }

    return (
        <FormModal
            open={open}
            onOpenChange={onOpenChange}
            title="Change Instructor"
        >
            <Form {...form}>
                <form
                    onSubmit={(e) => {
                        e.preventDefault();
                        void form.handleSubmit(() => void handleSubmit())(e);
                    }}
                >
                    <div className="space-y-4 pt-6">
                        <FormField
                            control={form.control}
                            name="instructor_id"
                            render={({ field }) => (
                                <FormItem className="space-y-2">
                                    <FormLabel>New Instructor</FormLabel>
                                    <Select
                                        value={field.value}
                                        onValueChange={field.onChange}
                                    >
                                        <FormControl>
                                            <SelectTrigger>
                                                <SelectValue placeholder="Select instructor" />
                                            </SelectTrigger>
                                        </FormControl>
                                        <SelectContent>
                                            {instructors.map((inst) => (
                                                <SelectItem
                                                    key={inst.id}
                                                    value={String(inst.id)}
                                                >
                                                    {inst.name_first}{' '}
                                                    {inst.name_last}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <FormField
                            control={form.control}
                            name="reason"
                            render={({ field }) => (
                                <FormItem className="space-y-2">
                                    <FormLabel>Reason for Change</FormLabel>
                                    <Select
                                        value={field.value}
                                        onValueChange={field.onChange}
                                    >
                                        <FormControl>
                                            <SelectTrigger>
                                                <SelectValue placeholder="Select a reason" />
                                            </SelectTrigger>
                                        </FormControl>
                                        <SelectContent>
                                            {Object.values(ChangeReason).map(
                                                (r) => (
                                                    <SelectItem
                                                        key={r}
                                                        value={r}
                                                    >
                                                        {r}
                                                    </SelectItem>
                                                )
                                            )}
                                        </SelectContent>
                                    </Select>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />

                        <FormField
                            control={form.control}
                            name="applyToFuture"
                            render={({ field }) => (
                                <FormItem className="flex items-center gap-2 space-y-0">
                                    <FormControl>
                                        <input
                                            type="checkbox"
                                            id="instructor-apply-to-future"
                                            checked={field.value}
                                            onChange={(e) =>
                                                field.onChange(e.target.checked)
                                            }
                                            className="size-4 rounded border-gray-300"
                                        />
                                    </FormControl>
                                    <label
                                        htmlFor="instructor-apply-to-future"
                                        className="text-sm font-normal cursor-pointer"
                                    >
                                        Apply this change to all future sessions
                                    </label>
                                </FormItem>
                            )}
                        />

                        <div className="flex justify-end gap-3 pt-4">
                            <Button
                                variant="outline"
                                type="button"
                                onClick={() => onOpenChange(false)}
                            >
                                Cancel
                            </Button>
                            <Button
                                type="submit"
                                disabled={submitting}
                                className="bg-brand text-white hover:bg-brand-dark"
                            >
                                {submitting ? 'Saving...' : 'Change Instructor'}
                            </Button>
                        </div>
                    </div>
                </form>
            </Form>
        </FormModal>
    );
}
