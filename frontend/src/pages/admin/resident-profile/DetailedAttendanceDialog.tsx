import { useState, useEffect } from 'react';
import { ChevronLeft, ChevronRight, Download } from 'lucide-react';
import useSWR from 'swr';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { DialogFooter } from '@/components/ui/dialog';
import { FormModal } from '@/components/shared';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow
} from '@/components/ui/table';
import {
    Tooltip,
    TooltipContent,
    TooltipTrigger
} from '@/components/ui/tooltip';
import { toast } from 'sonner';
import { ServerResponseMany } from '@/types';
import API from '@/api/api';
import { ResidentProgramOverview } from '@/types';
import { formatDate } from '@/lib/formatters';

interface AttendanceRecord {
    user_id: number;
    date: string;
    attendance_status: string;
    note: string;
    marked_by: string;
}

interface DetailedAttendanceDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    enrollment: ResidentProgramOverview | null;
    residentId: string;
}

const STATUS_DISPLAY: Record<string, { label: string; className: string }> = {
    present: {
        label: 'Present',
        className: 'bg-green-100 text-green-800 border-green-300'
    },
    partial: {
        label: 'Partial',
        className: 'bg-yellow-100 text-yellow-800 border-yellow-300'
    },
    absent: {
        label: 'Unexcused',
        className: 'bg-red-100 text-red-800 border-red-300'
    },
    absent_excused: {
        label: 'Excused',
        className: 'bg-yellow-100 text-yellow-800 border-yellow-300'
    },
    absent_unexcused: {
        label: 'Unexcused',
        className: 'bg-red-100 text-red-800 border-red-300'
    }
};

export function DetailedAttendanceDialog({
    open,
    onOpenChange,
    enrollment,
    residentId
}: DetailedAttendanceDialogProps) {
    const PAGE_SIZE = 10;
    const [page, setPage] = useState(1);

    useEffect(() => {
        if (open) setPage(1);
    }, [open]);

    const { data: attendanceResp } = useSWR<
        ServerResponseMany<AttendanceRecord>
    >(
        open && enrollment
            ? `/api/programs/${enrollment.program_id}/classes/${enrollment.cohort_id}/enrollments/${enrollment.enrollment_id}/attendance?per_page=500`
            : null
    );
    const records = attendanceResp?.data ?? [];
    const totalPages = Math.ceil(records.length / PAGE_SIZE);
    const paginatedRecords = records.slice(
        (page - 1) * PAGE_SIZE,
        page * PAGE_SIZE
    );

    if (!enrollment) return null;

    const handleExport = async () => {
        try {
            const { blob, headers } = await API.downloadFile(
                `program-users/${residentId}/attendance-export?cohort_id=${enrollment.cohort_id}`
            );
            const disposition = headers.get('Content-Disposition');
            const filename =
                disposition?.match(/filename="?([^"]+)"?/)?.[1] ??
                'attendance-export.csv';
            const url = window.URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = filename;
            document.body.appendChild(a);
            a.click();
            window.URL.revokeObjectURL(url);
            a.remove();
            toast.success('Attendance data exported to CSV');
        } catch {
            toast.error('Failed to export attendance data');
        }
    };

    return (
        <FormModal
            open={open}
            onOpenChange={onOpenChange}
            title={`Detailed Attendance - ${enrollment.class_name}`}
            description="Complete attendance record for this class"
            className="max-w-3xl"
            titleClassName="text-foreground"
        >
            <div className="py-4">
                <div className="mb-4 flex justify-end">
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void handleExport()}
                    >
                        <Download className="size-4 mr-2" />
                        Export to CSV
                    </Button>
                </div>
                <div className="border border-gray-200 rounded-lg overflow-hidden">
                    <Table className="table-fixed">
                        <TableHeader>
                            <TableRow>
                                <TableHead className="w-30">Date</TableHead>
                                <TableHead className="w-25">Status</TableHead>
                                <TableHead className="w-30">
                                    Marked By
                                </TableHead>
                                <TableHead>Notes</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {paginatedRecords.length > 0 ? (
                                paginatedRecords.map((record, i) => {
                                    const display =
                                        STATUS_DISPLAY[
                                            record.attendance_status
                                        ];
                                    return (
                                        <TableRow key={i}>
                                            <TableCell>
                                                {formatDate(record.date)}
                                            </TableCell>
                                            <TableCell>
                                                {display ? (
                                                    <Badge
                                                        className={
                                                            display.className
                                                        }
                                                    >
                                                        {display.label}
                                                    </Badge>
                                                ) : (
                                                    <Badge variant="outline">
                                                        {
                                                            record.attendance_status
                                                        }
                                                    </Badge>
                                                )}
                                            </TableCell>
                                            <TableCell className="text-sm text-gray-600">
                                                {record.marked_by || '\u2014'}
                                            </TableCell>
                                            <TableCell className="text-sm text-gray-600 truncate">
                                                {record.note ? (
                                                    <Tooltip>
                                                        <TooltipTrigger asChild>
                                                            <span className="truncate block">
                                                                {record.note}
                                                            </span>
                                                        </TooltipTrigger>
                                                        <TooltipContent
                                                            side="left"
                                                            className="max-w-[250px] break-words"
                                                        >
                                                            {record.note}
                                                        </TooltipContent>
                                                    </Tooltip>
                                                ) : (
                                                    '\u2014'
                                                )}
                                            </TableCell>
                                        </TableRow>
                                    );
                                })
                            ) : (
                                <TableRow>
                                    <TableCell
                                        colSpan={4}
                                        className="text-center text-sm text-gray-500 py-8"
                                    >
                                        No attendance records available
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </div>
                {totalPages > 1 && (
                    <div className="flex items-center justify-between mt-3">
                        <span className="text-sm text-gray-500">
                            Page {page} of {totalPages} ({records.length}{' '}
                            records)
                        </span>
                        <div className="flex items-center gap-1">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setPage((p) => p - 1)}
                                disabled={page === 1}
                            >
                                <ChevronLeft className="size-4" />
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setPage((p) => p + 1)}
                                disabled={page === totalPages}
                            >
                                <ChevronRight className="size-4" />
                            </Button>
                        </div>
                    </div>
                )}
            </div>
            <DialogFooter>
                <Button onClick={() => onOpenChange(false)}>Close</Button>
            </DialogFooter>
        </FormModal>
    );
}
