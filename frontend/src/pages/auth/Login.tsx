import LoginForm from '@/components/forms/LoginForm';
import { INIT_KRATOS_LOGIN_FLOW } from '@/types';
import { BRAND_GOLD } from '@/lib/brand';

export default function Login() {
    if (!window.location.search.includes('flow')) {
        window.location.href = INIT_KRATOS_LOGIN_FLOW;
        return null;
    }

    return (
        <div className="min-h-screen flex">
            <div className="hidden lg:flex lg:w-[55%] flex-col justify-between bg-[#2C3622] text-white p-14 relative overflow-hidden">
                <div
                    className="absolute top-0 left-0 w-130 h-130 rounded-full opacity-[0.07] -translate-x-1/3 -translate-y-1/3 pointer-events-none"
                    style={{ backgroundColor: BRAND_GOLD }}
                />
                <div
                    className="absolute bottom-0 right-0 w-105 h-105 rounded-full opacity-[0.07] translate-x-1/3 translate-y-1/3 pointer-events-none"
                    style={{ backgroundColor: BRAND_GOLD }}
                />

                <div className="relative z-10">
                    <img
                        src="/ul-logo-stacked-med-w.svg"
                        alt="UnlockEd"
                        className="h-20 object-contain"
                    />
                </div>

                <div className="relative z-10 space-y-6 max-w-lg my-auto">
                    <h1 className="text-5xl font-bold leading-tight tracking-tight">
                        Build from the
                        <br />
                        <span style={{ color: BRAND_GOLD }}>inside, out.</span>
                    </h1>
                    <p className="text-lg text-slate-300 leading-relaxed">
                        We build tools that turn rehabilitation into reality:
                        enabling a justice system that is built on healing,
                        grounded in safety, and powered by accountability.
                    </p>
                </div>
            </div>

            <div className="flex-1 flex flex-col items-center justify-center bg-muted px-8 py-12">
                <div className="lg:hidden mb-8 text-center">
                    <img
                        className="h-16 mx-auto block dark:hidden"
                        src="/ul-logo-stacked-med-d.svg"
                        alt="UnlockEd"
                    />
                    <img
                        className="h-16 mx-auto hidden dark:block"
                        src="/ul-logo-stacked-med-w.svg"
                        alt="UnlockEd"
                    />
                </div>

                <div className="w-full max-w-sm">
                    <div className="mb-6">
                        <h2 className="text-2xl font-bold text-foreground">
                            Welcome back
                        </h2>
                        <p className="text-muted-foreground mt-1 text-sm">
                            Sign in to your UnlockEd account
                        </p>
                    </div>
                    <div className="bg-card shadow-md rounded-xl px-6 py-6 border border-border/50">
                        <LoginForm />
                    </div>
                </div>
            </div>
        </div>
    );
}
