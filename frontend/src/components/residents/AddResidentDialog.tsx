import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import API from '@/api/api';
import { Facility, NewUserResponse, ServerResponseOne, User } from '@/types';
import { addResidentSchema, AddResidentInput } from '@/lib/validation';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
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
import { FormModal, ResetPasswordModal } from '@/components/shared';

interface AddResidentDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    facilities: Facility[];
    showFacilityColumn: boolean;
    defaultFacilityId?: number;
    onSuccess: (user?: User) => void;
}

export function AddResidentDialog({
    open,
    onOpenChange,
    facilities,
    showFacilityColumn,
    defaultFacilityId,
    onSuccess
}: AddResidentDialogProps) {
    const form = useForm<AddResidentInput>({
        resolver: zodResolver(addResidentSchema),
        defaultValues: {
            name_first: '',
            name_last: '',
            username: '',
            doc_id: '',
            facility_id: defaultFacilityId
                ? String(defaultFacilityId)
                : undefined
        }
    });
    const [submitting, setSubmitting] = useState(false);
    const [showPasswordModal, setShowPasswordModal] = useState(false);
    const [tempPassword, setTempPassword] = useState('');
    const [createdUserName, setCreatedUserName] = useState('');
    const [kratosRegistered, setKratosRegistered] = useState(true);

    useEffect(() => {
        if (open) {
            form.reset({
                name_first: '',
                name_last: '',
                username: '',
                doc_id: '',
                facility_id: defaultFacilityId
                    ? String(defaultFacilityId)
                    : undefined
            });
        }
    }, [open, defaultFacilityId, form]);

    const handleAddUser = async (formData: AddResidentInput) => {
        if (submitting) return;
        setSubmitting(true);
        try {
            const user = {
                name_first: formData.name_first,
                name_last: formData.name_last,
                username: formData.username,
                doc_id: formData.doc_id,
                role: 'student' as const,
                ...(formData.facility_id
                    ? { facility_id: Number(formData.facility_id) }
                    : {})
            };
            const response = (await API.post<NewUserResponse, object>('users', {
                user
            })) as ServerResponseOne<NewUserResponse>;

            if (response.success) {
                onOpenChange(false);
                toast.success(
                    `Resident ${formData.name_first} ${formData.name_last} added successfully`
                );
                form.reset();
                setCreatedUserName(
                    `${formData.name_first} ${formData.name_last}`
                );
                setTempPassword(response.data.temp_password);
                setKratosRegistered(response.data.kratos_registered);
                setShowPasswordModal(true);
                onSuccess(response.data.user);
            } else if (response.status === 409) {
                form.setError('doc_id', { message: response.message });
            } else {
                toast.error(response.message ?? 'Failed to create resident');
            }
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <>
            <FormModal
                open={open}
                onOpenChange={onOpenChange}
                title="Add New Resident"
                description="Create a new resident profile in the system"
                titleClassName="text-foreground"
            >
                <Form {...form}>
                    <form
                        onSubmit={(e) => {
                            e.preventDefault();
                            void form.handleSubmit(
                                (d) => void handleAddUser(d)
                            )(e);
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
                                                    placeholder="First name"
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
                                                    placeholder="Last name"
                                                    {...field}
                                                />
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                            </div>
                            <FormField
                                control={form.control}
                                name="username"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Username</FormLabel>
                                        <FormControl>
                                            <Input
                                                placeholder="Enter username for login"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                            <FormField
                                control={form.control}
                                name="doc_id"
                                render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Resident ID</FormLabel>
                                        <FormControl>
                                            <Input
                                                placeholder="e.g., R001"
                                                {...field}
                                            />
                                        </FormControl>
                                        <FormMessage />
                                    </FormItem>
                                )}
                            />
                            {showFacilityColumn && (
                                <FormField
                                    control={form.control}
                                    name="facility_id"
                                    render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>Facility</FormLabel>
                                            <Select
                                                value={field.value}
                                                onValueChange={field.onChange}
                                            >
                                                <FormControl>
                                                    <SelectTrigger>
                                                        <SelectValue placeholder="Select facility" />
                                                    </SelectTrigger>
                                                </FormControl>
                                                <SelectContent>
                                                    {facilities.map((f) => (
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
                            )}
                        </div>
                        <DialogFooter className="pt-4">
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
                                variant="brand"
                            >
                                {submitting ? 'Adding...' : 'Add Resident'}
                            </Button>
                        </DialogFooter>
                    </form>
                </Form>
            </FormModal>
            <ResetPasswordModal
                open={showPasswordModal}
                onOpenChange={(isOpen) => {
                    setShowPasswordModal(isOpen);
                    if (!isOpen) {
                        setTempPassword('');
                        setCreatedUserName('');
                        setKratosRegistered(true);
                    }
                }}
                name={createdUserName}
                subject="resident"
                presetPassword={tempPassword}
                resultTitle="New Password"
                warning={
                    kratosRegistered
                        ? undefined
                        : "This resident's login could not be registered. Use Reset Password before sharing this with the resident."
                }
            />
        </>
    );
}
