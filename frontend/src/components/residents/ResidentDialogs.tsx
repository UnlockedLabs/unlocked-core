import { useState, useEffect, useMemo } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import useSWR from 'swr';
import API from '@/api/api';
import { User, Facility, ValidResident, ServerResponseOne } from '@/types';
import {
    editResidentSchema,
    EditResidentInput,
    buildConfirmResidentIdSchema,
    ConfirmResidentIdInput,
    buildTransferResidentSchema,
    TransferResidentInput
} from '@/lib/validation';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { DialogFooter } from '@/components/ui/dialog';
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
import { FormModal, TonedPanel } from '@/components/shared';
import { Check } from 'lucide-react';

/**
 * Type-to-confirm token for a resident. Falls back to the username (with a
 * matching label) when the resident has no Resident ID, per ticket 674.
 */
function confirmTokenFor(resident: User) {
    const hasDocId = Boolean(resident.doc_id);
    return {
        confirmToken: hasDocId ? resident.doc_id! : resident.username,
        confirmLabel: hasDocId ? 'Resident ID' : 'username'
    };
}

// ---------------------------------------------------------------------------
// EditResidentDialog
// ---------------------------------------------------------------------------

interface EditResidentDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    resident: User;
    onSuccess: () => void;
}

export function EditResidentDialog({
    open,
    onOpenChange,
    resident,
    onSuccess
}: EditResidentDialogProps) {
    const form = useForm<EditResidentInput>({
        resolver: zodResolver(editResidentSchema),
        defaultValues: {
            name_first: '',
            name_last: '',
            doc_id: ''
        }
    });

    useEffect(() => {
        if (open) {
            form.reset({
                name_first: resident.name_first,
                name_last: resident.name_last,
                doc_id: resident.doc_id ?? ''
            });
        }
    }, [open, resident, form]);

    const handleSave = async (data: EditResidentInput) => {
        const response = await API.patch<User, object>(
            `users/${resident.id}`,
            data
        );
        if (response.success) {
            toast.success(
                `${resident.name_first} ${resident.name_last}'s profile updated`
            );
            onOpenChange(false);
            onSuccess();
        } else if (response.status === 409) {
            form.setError('doc_id', { message: response.message });
        } else {
            toast.error(response.message ?? 'Failed to update resident');
        }
    };

    return (
        <FormModal
            open={open}
            onOpenChange={onOpenChange}
            title="Edit Resident Profile"
            description="Update resident information"
            titleClassName="text-foreground"
        >
            <Form {...form}>
                <form
                    onSubmit={(e) => {
                        e.preventDefault();
                        void form.handleSubmit((d) => void handleSave(d))(e);
                    }}
                >
                    <div className="space-y-4 py-4">
                        <div className="grid grid-cols-2 gap-4">
                            <FormField
                                control={form.control}
                                name="name_first"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>First Name</FormLabel>
                                        <FormControl>
                                            <Input
                                                className="mt-2"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                            <FormField
                                control={form.control}
                                name="name_last"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Last Name</FormLabel>
                                        <FormControl>
                                            <Input
                                                className="mt-2"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>
                        <div>
                            <Label htmlFor="edit-username">Username</Label>
                            <Input
                                id="edit-username"
                                value={resident.username}
                                disabled
                                className="mt-2 bg-muted"
                            />
                        </div>
                        <FormField
                            control={form.control}
                            name="doc_id"
                            render={({ field }) => (
                                <FormItem>
                                    <FormLabel>Resident ID</FormLabel>
                                    <FormControl>
                                        <Input className="mt-2" {...field} />
                                    </FormControl>
                                    <FormMessage />
                                </FormItem>
                            )}
                        />
                    </div>
                    <DialogFooter>
                        <Button
                            variant="outline"
                            type="button"
                            onClick={() => onOpenChange(false)}
                        >
                            Cancel
                        </Button>
                        <Button type="submit" variant="brand">
                            Save Changes
                        </Button>
                    </DialogFooter>
                </form>
            </Form>
        </FormModal>
    );
}

// ---------------------------------------------------------------------------
// DeactivateDialog
// ---------------------------------------------------------------------------

interface DeactivateDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    resident: User;
    onSuccess: () => void;
}

export function DeactivateDialog({
    open,
    onOpenChange,
    resident,
    onSuccess
}: DeactivateDialogProps) {
    const [loading, setLoading] = useState(false);
    const { confirmToken: displayId, confirmLabel } = confirmTokenFor(resident);
    const form = useForm<ConfirmResidentIdInput>({
        resolver: zodResolver(
            buildConfirmResidentIdSchema(displayId, confirmLabel)
        ),
        mode: 'onChange',
        defaultValues: { confirm: '' }
    });

    useEffect(() => {
        if (!open) form.reset({ confirm: '' });
    }, [open, form]);

    const handleDeactivate = async () => {
        setLoading(true);
        const response = await API.post<string, object>(
            `users/${resident.id}/deactivate`,
            {}
        );
        setLoading(false);

        if (response.success) {
            toast.success(
                `${resident.name_first} ${resident.name_last} has been deactivated`
            );
            onOpenChange(false);
            onSuccess();
        } else {
            toast.error(response.message ?? 'Failed to deactivate resident');
        }
    };

    return (
        <FormModal
            open={open}
            onOpenChange={onOpenChange}
            title="Deactivate Account"
            description={`You are about to deactivate ${resident.name_first} ${resident.name_last}'s account.`}
            titleClassName="text-foreground"
            preventOutsideClose
        >
            <Form {...form}>
                <form
                    onSubmit={(e) => {
                        e.preventDefault();
                        void form.handleSubmit(() => void handleDeactivate())(
                            e
                        );
                    }}
                >
                    <div className="py-4 space-y-4">
                        <ul className="space-y-2 text-sm text-gray-700">
                            <li className="flex gap-2">
                                <span className="text-gray-400">&bull;</span>
                                <span>
                                    The resident will be withdrawn from all
                                    active classes and programs.
                                </span>
                            </li>
                            <li className="flex gap-2">
                                <span className="text-gray-400">&bull;</span>
                                <span>
                                    The resident&apos;s account will be locked
                                    and marked as Deactivated.
                                </span>
                            </li>
                            <li className="flex gap-2">
                                <span className="text-gray-400">&bull;</span>
                                <span>
                                    Staff will no longer be able to edit this
                                    resident&apos;s account.
                                </span>
                            </li>
                            <li className="flex gap-2">
                                <span className="text-gray-400">&bull;</span>
                                <span>
                                    The resident will not be able to log in or
                                    enroll in new programs.
                                </span>
                            </li>
                            <li className="flex gap-2">
                                <span className="text-gray-400">&bull;</span>
                                <span>
                                    The time this account was deactivated will
                                    be recorded.
                                </span>
                            </li>
                            <li className="flex gap-2">
                                <span className="text-gray-400">&bull;</span>
                                <span>
                                    The resident&apos;s account history and
                                    favorites will be preserved and remain
                                    searchable.
                                </span>
                            </li>
                        </ul>

                        <TonedPanel tone="orange">
                            <FormField
                                control={form.control}
                                name="confirm"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel htmlFor="deactivate-confirm">
                                            To confirm, type the {confirmLabel}:{' '}
                                            <strong>{displayId}</strong>
                                        </FormLabel>
                                        <FormControl>
                                            <Input
                                                id="deactivate-confirm"
                                                placeholder="Type Resident ID to confirm"
                                                className="mt-2"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </TonedPanel>
                    </div>
                    <DialogFooter>
                        <Button
                            variant="outline"
                            type="button"
                            onClick={() => onOpenChange(false)}
                        >
                            Cancel
                        </Button>
                        <Button
                            type="submit"
                            disabled={loading || !form.formState.isValid}
                            variant="warning"
                        >
                            Deactivate Account
                        </Button>
                    </DialogFooter>
                </form>
            </Form>
        </FormModal>
    );
}

// ---------------------------------------------------------------------------
// DeleteDialog
// ---------------------------------------------------------------------------

interface DeleteDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    resident: User;
    onSuccess: () => void;
}

export function DeleteDialog({
    open,
    onOpenChange,
    resident,
    onSuccess
}: DeleteDialogProps) {
    const [loading, setLoading] = useState(false);
    const { confirmToken: displayId, confirmLabel } = confirmTokenFor(resident);
    const form = useForm<ConfirmResidentIdInput>({
        resolver: zodResolver(
            buildConfirmResidentIdSchema(displayId, confirmLabel)
        ),
        mode: 'onChange',
        defaultValues: { confirm: '' }
    });

    useEffect(() => {
        if (!open) form.reset({ confirm: '' });
    }, [open, form]);

    const handleDelete = async () => {
        setLoading(true);
        const response = await API.delete(`users/${resident.id}`);
        setLoading(false);

        if (response.success) {
            toast.success(
                `${resident.name_first} ${resident.name_last} has been deleted`
            );
            onOpenChange(false);
            onSuccess();
        } else {
            toast.error(response.message ?? 'Failed to delete resident');
        }
    };

    return (
        <FormModal
            open={open}
            onOpenChange={onOpenChange}
            title="Delete Resident"
            description={`Are you sure you want to delete ${resident.name_first} ${resident.name_last}?`}
            titleClassName="text-foreground"
            preventOutsideClose
        >
            <Form {...form}>
                <form
                    onSubmit={(e) => {
                        e.preventDefault();
                        void form.handleSubmit(() => void handleDelete())(e);
                    }}
                >
                    <div className="py-4 space-y-4">
                        <p className="text-sm text-red-600 font-medium">
                            This action cannot be undone. All data associated
                            with this resident will be permanently deleted.
                        </p>

                        <TonedPanel tone="red">
                            <FormField
                                control={form.control}
                                name="confirm"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel htmlFor="delete-confirm">
                                            To confirm, type the {confirmLabel}:{' '}
                                            <strong>{displayId}</strong>
                                        </FormLabel>
                                        <FormControl>
                                            <Input
                                                id="delete-confirm"
                                                placeholder="Type Resident ID to confirm"
                                                className="mt-2"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </TonedPanel>
                    </div>
                    <DialogFooter>
                        <Button
                            variant="outline"
                            type="button"
                            onClick={() => onOpenChange(false)}
                        >
                            Cancel
                        </Button>
                        <Button
                            type="submit"
                            disabled={loading || !form.formState.isValid}
                            variant="destructive"
                        >
                            Delete Resident
                        </Button>
                    </DialogFooter>
                </form>
            </Form>
        </FormModal>
    );
}

// ---------------------------------------------------------------------------
// TransferDialog
// ---------------------------------------------------------------------------

interface TransferDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    resident: User;
    facilities: Facility[];
    onSuccess: () => void;
}

export function TransferDialog({
    open,
    onOpenChange,
    resident,
    facilities,
    onSuccess
}: TransferDialogProps) {
    const [loading, setLoading] = useState(false);
    const { confirmToken: displayId, confirmLabel } = confirmTokenFor(resident);
    const schema = useMemo(
        () => buildTransferResidentSchema(displayId, confirmLabel),
        [displayId, confirmLabel]
    );
    const form = useForm<TransferResidentInput>({
        resolver: zodResolver(schema),
        mode: 'onChange',
        defaultValues: { facility_id: '', confirm: '' }
    });
    const transferFacilityId = form.watch('facility_id');

    useEffect(() => {
        if (!open) form.reset({ facility_id: '', confirm: '' });
    }, [open, form]);

    const { data: verifyResp } = useSWR<ServerResponseOne<ValidResident>>(
        open && transferFacilityId
            ? `/api/users/resident-verify?user_id=${resident.id}&facility_id=${transferFacilityId}&doc_id=${encodeURIComponent(resident.doc_id ?? '')}`
            : null
    );
    const rawConflicts = verifyResp?.data?.program_names ?? [];
    const programConflicts = [
        ...new Set(rawConflicts.map((c) => c.program_name))
    ];

    const currentFacilityName =
        resident.facility?.name ??
        facilities.find((f) => f.id === resident.facility_id)?.name ??
        'Unknown';
    const targetFacilityName =
        facilities.find((f) => String(f.id) === transferFacilityId)?.name ?? '';

    const handleTransfer = async () => {
        setLoading(true);
        const response = await API.patch<string, object>(
            'users/resident-transfer',
            {
                user_id: resident.id,
                curr_facility_id: resident.facility?.id ?? resident.facility_id,
                trans_facility_id: Number(transferFacilityId)
            }
        );
        setLoading(false);

        if (response.success) {
            toast.success(
                `${resident.name_first} ${resident.name_last} transferred to ${targetFacilityName}`
            );
            onOpenChange(false);
            onSuccess();
        } else {
            toast.error(response.message ?? 'Failed to transfer resident');
        }
    };

    return (
        <FormModal
            open={open}
            onOpenChange={onOpenChange}
            title="Transfer Resident"
            description={`Move ${resident.name_last}, ${resident.name_first} to a different facility`}
            className="max-w-2xl max-h-[90vh] flex flex-col"
            titleClassName="text-foreground"
            headerClassName="shrink-0"
            preventOutsideClose
        >
            <Form {...form}>
                <form
                    onSubmit={(e) => {
                        e.preventDefault();
                        void form.handleSubmit(() => void handleTransfer())(e);
                    }}
                >
                    <div className="space-y-6 py-4 flex-1 min-h-0 overflow-y-auto pr-1">
                        <div className="space-y-3">
                            <div className="text-sm text-gray-600">
                                Current Facility:{' '}
                                <span className="font-medium text-gray-900">
                                    {currentFacilityName}
                                </span>
                            </div>
                            <FormField
                                control={form.control}
                                name="facility_id"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel className="text-xs text-gray-500">
                                            New Facility
                                        </FormLabel>
                                        <Select
                                            value={field.value}
                                            onValueChange={field.onChange}
                                        >
                                            <FormControl>
                                                <SelectTrigger className="mt-1">
                                                    <SelectValue placeholder="Select facility" />
                                                </SelectTrigger>
                                            </FormControl>
                                            <SelectContent>
                                                {facilities
                                                    .filter(
                                                        (f) =>
                                                            f.id !==
                                                            (resident.facility
                                                                ?.id ??
                                                                resident.facility_id)
                                                    )
                                                    .map((f) => (
                                                        <SelectItem
                                                            key={f.id}
                                                            value={String(f.id)}
                                                        >
                                                            {f.name}
                                                        </SelectItem>
                                                    ))}
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                        </div>

                        {transferFacilityId && (
                            <>
                                <TonedPanel tone="orange">
                                    <div className="font-medium text-orange-900 text-sm mb-1">
                                        Transfer will unenroll resident from all
                                        classes
                                    </div>
                                    <div className="text-sm text-orange-800">
                                        {resident.name_first} will be removed
                                        from all active enrollments and must be
                                        re-enrolled at the new facility.
                                    </div>
                                </TonedPanel>

                                {programConflicts.length > 0 && (
                                    <div className="border border-gray-200 rounded-lg p-4">
                                        <div className="font-medium text-sm text-gray-900 mb-3">
                                            Programs not available at{' '}
                                            {targetFacilityName}
                                        </div>
                                        <div className="space-y-2">
                                            {programConflicts.map(
                                                (name, idx) => (
                                                    <div
                                                        key={idx}
                                                        className="text-sm text-gray-600 flex items-center gap-2"
                                                    >
                                                        <div className="size-1.5 rounded-full bg-gray-400" />
                                                        {name}
                                                    </div>
                                                )
                                            )}
                                        </div>
                                    </div>
                                )}

                                <div className="space-y-2">
                                    <div className="text-xs font-medium text-gray-500 uppercase tracking-wide">
                                        What happens next
                                    </div>
                                    <div className="space-y-1.5 text-sm text-gray-700">
                                        <div className="flex items-center gap-2">
                                            <Check className="size-4 text-green-600 shrink-0" />
                                            <span>
                                                Account history and favorites
                                                will be preserved
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <Check className="size-4 text-green-600 shrink-0" />
                                            <span>
                                                Resident can log in at{' '}
                                                {targetFacilityName}
                                            </span>
                                        </div>
                                    </div>
                                </div>

                                <div className="border-t pt-4">
                                    <FormField
                                        control={form.control}
                                        name="confirm"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel
                                                    htmlFor="transfer-confirm"
                                                    className="text-sm font-medium"
                                                >
                                                    Type the {confirmLabel}{' '}
                                                    <span className="font-mono font-semibold text-brand-dark">
                                                        {displayId}
                                                    </span>{' '}
                                                    to confirm
                                                </FormLabel>
                                                <FormControl>
                                                    <Input
                                                        id="transfer-confirm"
                                                        placeholder="Enter Resident ID"
                                                        className="mt-2"
                                                        {...field}
                                                    />
                                                </FormControl>
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                </div>
                            </>
                        )}
                    </div>
                    <DialogFooter className="shrink-0">
                        <Button
                            variant="outline"
                            type="button"
                            onClick={() => onOpenChange(false)}
                        >
                            Cancel
                        </Button>
                        <Button
                            type="submit"
                            disabled={loading || !form.formState.isValid}
                            variant="brand"
                        >
                            Transfer Resident
                        </Button>
                    </DialogFooter>
                </form>
            </Form>
        </FormModal>
    );
}
