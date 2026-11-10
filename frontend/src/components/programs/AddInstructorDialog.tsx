import { useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import API from '@/api/api';
import { NewUserResponse, ServerResponseOne, User } from '@/types';
import { addInstructorSchema, AddInstructorInput } from '@/lib/validation';
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
import { FormModal, ResetPasswordModal } from '@/components/shared';

// The server's messages for a bad name or username start with the field's
// label, so they can be shown under that field.
const fieldForServerError: [string, keyof AddInstructorInput][] = [
    ['First name', 'name_first'],
    ['Last name', 'name_last'],
    ['Username', 'username']
];

interface AddInstructorDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    facilityId: number;
    onCreated: (user: User) => void;
}

export function AddInstructorDialog({
    open,
    onOpenChange,
    facilityId,
    onCreated
}: AddInstructorDialogProps) {
    const form = useForm<AddInstructorInput>({
        resolver: zodResolver(addInstructorSchema),
        defaultValues: { name_first: '', name_last: '', username: '' }
    });
    const [submitting, setSubmitting] = useState(false);
    const [showPasswordModal, setShowPasswordModal] = useState(false);
    const [tempPassword, setTempPassword] = useState('');
    const [createdUserName, setCreatedUserName] = useState('');
    const [kratosRegistered, setKratosRegistered] = useState(true);

    useEffect(() => {
        if (open) {
            form.reset({ name_first: '', name_last: '', username: '' });
        }
    }, [open, form]);

    const handleAddInstructor = async (formData: AddInstructorInput) => {
        if (submitting) return;
        setSubmitting(true);
        try {
            const response = (await API.post<NewUserResponse, object>('users', {
                user: {
                    name_first: formData.name_first,
                    name_last: formData.name_last,
                    username: formData.username,
                    email: '',
                    role: 'facility_admin',
                    facility_id: facilityId
                },
                provider_platforms: []
            })) as ServerResponseOne<NewUserResponse>;

            if (response.success) {
                onOpenChange(false);
                toast.success(
                    `Instructor ${formData.name_first} ${formData.name_last} added successfully`
                );
                setCreatedUserName(
                    `${formData.name_first} ${formData.name_last}`
                );
                setTempPassword(response.data.temp_password);
                setKratosRegistered(response.data.kratos_registered);
                setShowPasswordModal(true);
                onCreated(response.data.user);
                return;
            }
            const message = response.message ?? 'Failed to create instructor';
            const field = fieldForServerError.find(([label]) =>
                message.startsWith(label)
            )?.[1];
            if (field) {
                form.setError(field, { message });
            } else {
                form.setError('root', { message });
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
                title="Add Instructor"
                description="Create a facility admin at this class's facility"
                titleClassName="text-foreground"
            >
                <Form {...form}>
                    <form
                        onSubmit={(e) => {
                            e.preventDefault();
                            // The class form this opens from is a form too, so
                            // keep this submit from reaching it.
                            e.stopPropagation();
                            void form.handleSubmit(
                                (d) => void handleAddInstructor(d)
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
                            {form.formState.errors.root && (
                                <p className="text-sm text-destructive">
                                    {form.formState.errors.root.message}
                                </p>
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
                                {submitting ? 'Adding...' : 'Add Instructor'}
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
                subject="administrator"
                presetPassword={tempPassword}
                resultTitle="New Password"
                warning={
                    kratosRegistered
                        ? undefined
                        : "This instructor's login could not be registered. Use Reset Password on the Admins page before sharing this with them."
                }
            />
        </>
    );
}
