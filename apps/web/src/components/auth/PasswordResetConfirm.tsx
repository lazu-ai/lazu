import { CheckCircle2, Mail, ShieldCheck } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { z } from 'zod';
import { AuthPanel, AuthShell, AuthTextField } from './AuthLayout';
import { useSession } from '../../context/User';
import { API, updateAPI } from '../../helpers/api';
import { setUserData } from '../../helpers/data';
import { showError, showNotice } from '../../helpers/utils';
import { readApiError, unwrap } from '../../lib/api-error';
import { Button, InlineNotice } from '../ui';
import type { SubmitHandler } from 'react-hook-form';

type PasswordResetConfirmValues = {
  email: string;
  token: string;
  password: string;
  confirmPassword: string;
};

type ResetRequestConfig = NonNullable<Parameters<typeof API.post>[2]> & {
  skipErrorHandler?: boolean;
};

const passwordCharacterCount = (value: string) => Array.from(value).length;
const passwordByteCount = (value: string) =>
  new TextEncoder().encode(value).byteLength;

const PasswordResetConfirm = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const userSession = useSession();
  const [searchParams] = useSearchParams();
  const [loading, setLoading] = useState(false);
  const [completed, setCompleted] = useState(false);
  const {
    formState: { errors },
    handleSubmit,
    register,
    reset,
    setError,
  } = useForm<PasswordResetConfirmValues>({
    defaultValues: {
      email: '',
      token: '',
      password: '',
      confirmPassword: '',
    },
  });

  const email = searchParams.get('email')?.trim() || '';
  const token = searchParams.get('token')?.trim() || '';
  const userIdParam = searchParams.get('user_id')?.trim() || '';
  const sessionVersionParam = searchParams.get('session_version')?.trim() || '';
  const userId = Number(userIdParam);
  const issuedSessionVersion = Number(sessionVersionParam);
  const isValidResetLink = Boolean(
    email &&
    token &&
    /^\d+$/.test(userIdParam) &&
    Number.isSafeInteger(userId) &&
    userId > 0 &&
    /^\d+$/.test(sessionVersionParam) &&
    Number.isSafeInteger(issuedSessionVersion) &&
    issuedSessionVersion >= 0,
  );

  const schema = useMemo(
    () =>
      z
        .object({
          email: z
            .string()
            .trim()
            .email(t('page.public.auth.errors.emailInvalid')),
          token: z
            .string()
            .trim()
            .min(1, t('page.public.auth.errors.tokenMissing')),
          password: z.string().superRefine((value, context) => {
            if (!value) {
              context.addIssue({
                code: 'custom',
                message: t('page.public.auth.errors.passwordRequired'),
              });
              return;
            }
            const characterCount = passwordCharacterCount(value);
            if (characterCount < 8) {
              context.addIssue({
                code: 'custom',
                message: t('page.public.auth.errors.passwordMinLength'),
              });
            } else if (characterCount > 64) {
              context.addIssue({
                code: 'custom',
                message: t('page.public.auth.errors.passwordMaxLength'),
              });
            }
            if (passwordByteCount(value) > 72) {
              context.addIssue({
                code: 'custom',
                message: t('page.public.auth.errors.passwordByteLimit'),
              });
            }
          }),
          confirmPassword: z
            .string()
            .min(1, t('page.public.auth.errors.confirmPasswordRequired')),
        })
        .refine((values) => values.password === values.confirmPassword, {
          message: t('page.public.auth.errors.passwordMismatch'),
          path: ['confirmPassword'],
        }),
    [t],
  );

  const setIssueError = (issue: z.ZodIssue) => {
    const field = issue.path[0];
    if (
      field === 'email' ||
      field === 'token' ||
      field === 'password' ||
      field === 'confirmPassword'
    ) {
      setError(field, { message: issue.message, type: 'manual' });
    }
  };

  useEffect(() => {
    reset({ email, token, password: '', confirmPassword: '' });
    setCompleted(false);
  }, [email, reset, token]);

  const resetPassword: SubmitHandler<PasswordResetConfirmValues> = async (
    values,
  ) => {
    const parsed = schema.safeParse(values);
    if (!parsed.success) {
      parsed.error.issues.forEach(setIssueError);
      return;
    }

    setLoading(true);
    try {
      const data = unwrap<unknown>(
        await API.post(
          '/api/user/reset/v2',
          {
            email: parsed.data.email,
            user_id: userId,
            session_version: issuedSessionVersion,
            token: parsed.data.token,
            password: parsed.data.password,
          },
          { skipErrorHandler: true } satisfies ResetRequestConfig,
        ),
      );
      // A server that still answers with a generated password predates the
      // chosen-password flow; the reader must not treat that as done.
      if (typeof data === 'string' && data.length > 0) {
        showError(t('page.public.auth.errors.passwordResetUpgradeRequired'));
        return;
      }
      showNotice(t('page.public.auth.passwordResetComplete'));
      if (data && typeof data === 'object' && !Array.isArray(data)) {
        const user = data as Record<string, unknown>;
        userSession.signIn(user);
        localStorage.setItem('user', JSON.stringify(user));
        setUserData(user);
        updateAPI();
        navigate('/console');
        return;
      }
      setCompleted(true);
    } catch (error) {
      // A server without the v2 route answers a bare 404 with no error code.
      const info = readApiError(error);
      if (info.status === 404 && !info.code) {
        showError(t('page.public.auth.errors.passwordResetUpgradeRequired'));
      } else {
        showError(error, t('page.public.auth.errors.passwordResetFailed'));
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <AuthShell>
      <AuthPanel
        title={
          completed
            ? t('page.public.auth.passwordResetComplete')
            : t('page.public.auth.resetConfirmTitle')
        }
        description={
          completed
            ? t('page.public.auth.backToLogin')
            : t('page.public.auth.resetConfirmDescription')
        }
        footer={
          <Link to='/login' className='text-[var(--ui-accent)]'>
            {t('page.public.auth.backToLogin')}
          </Link>
        }
      >
        {completed ? (
          <InlineNotice
            title={t('page.public.auth.passwordResetComplete')}
            tone='success'
          />
        ) : !isValidResetLink ? (
          <div className='mb-4 space-y-4'>
            <InlineNotice
              title={t('page.public.auth.errors.invalidResetLink')}
              tone='danger'
            />
            <Button fullWidth render={<Link to='/reset' />} variant='secondary'>
              {t('page.public.auth.resetTitle')}
            </Button>
          </div>
        ) : (
          <form className='auth-form' onSubmit={handleSubmit(resetPassword)}>
            <AuthTextField
              autoComplete='username'
              error={errors.email?.message}
              icon={Mail}
              id='confirm-email'
              label={t('page.public.auth.email')}
              placeholder={t('page.public.auth.emailPending')}
              readOnly
              register={register('email')}
              type='email'
            />
            <input type='hidden' {...register('token')} />

            <AuthTextField
              autoComplete='new-password'
              error={errors.password?.message}
              id='new-password'
              label={t('page.public.auth.newPassword')}
              placeholder={t('page.public.auth.passwordCreatePlaceholder')}
              register={register('password')}
              type='password'
            />
            <AuthTextField
              autoComplete='new-password'
              error={errors.confirmPassword?.message}
              id='confirm-password'
              label={t('page.public.auth.confirmPassword')}
              placeholder={t('page.public.auth.confirmPasswordPlaceholder')}
              register={register('confirmPassword')}
              type='password'
            />

            <Button fullWidth loading={loading} type='submit' variant='primary'>
              {t('page.public.auth.confirmResetPassword')}
            </Button>
          </form>
        )}
      </AuthPanel>
    </AuthShell>
  );
};

export default PasswordResetConfirm;
