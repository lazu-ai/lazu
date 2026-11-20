import { useQueryClient } from '@tanstack/react-query';
import { TriangleAlert } from 'lucide-react';
import {
  useEffect,
  useMemo,
  useState,
  type MutableRefObject,
  type ReactNode,
} from 'react';
import { infoSourceLabel } from './modelInfo';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Select, SelectItem } from '../../components/ui/select';
import { API } from '../../helpers/api';
import { showError } from '../../helpers/utils';
import {
  useModelInfoMutations,
  useModelInfoReviewsQuery,
  useModelMakersQuery,
} from '../../hooks/models/useModelInfoQuery';
import { makerFullLabel } from '../../lib/model-makers';
import type { ModelView } from './modelView';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps.info';

type Props = {
  t: TFunction;
  view: ModelView;
  /** 锁定资料 sits in the form's footer; the panel saves it. */
  locked: boolean;
  /** 编辑资料 saves this section with the rest of the form: the panel
   *  publishes its save here (null when nothing changed). */
  saveRef: MutableRefObject<(() => Promise<boolean>) | null>;
};

type Record_ = Record<string, unknown>;

/**
 * The two facts an operator edits by hand, maker and release date, each with
 * where it came from, and a release date models.dev disagrees with. The rest
 * of 资料 is read in the overview; the lock makes the daily sync only propose.
 */
export default function ModelInfoPanel({ locked, saveRef, t, view }: Props) {
  const record = view.record as Record_;
  const queryClient = useQueryClient();
  const makers = useModelMakersQuery();
  const reviews = useModelInfoReviewsQuery();
  const { resolveMutation } = useModelInfoMutations(t);
  const sources = (record.info_sources as Record<string, string>) || {};

  const [maker, setMaker] = useState('');
  const [releaseDate, setReleaseDate] = useState('');

  useEffect(() => {
    setMaker(String(record.maker || record.maker_resolved || ''));
    setReleaseDate(String(record.release_date || ''));
  }, [record.id, record.maker, record.maker_resolved, record.release_date]);

  const pending = useMemo(
    () =>
      (reviews.data?.items || []).filter(
        (row) =>
          row.model_name === view.name &&
          row.status === 'pending' &&
          (row.kind === 'diff' || row.kind === 'first_run'),
      ),
    [reviews.data, view.name],
  );
  const pendingFor = (field: string) =>
    pending.find((row) => row.field === field);
  const releaseReview = pendingFor('release_date');

  const dirty =
    maker !== String(record.maker || record.maker_resolved || '') ||
    releaseDate !== String(record.release_date || '') ||
    locked !== Boolean(record.info_locked);

  const save = async () => {
    try {
      // Only the facts this section edits travel; the whole-record model
      // update would also rewrite columns it never showed.
      const body: Record_ = {
        id: Number(record.id),
        info_locked: locked,
        // An inferred maker shown in the picker is not a choice the operator
        // made; only a real change is written (and then marked 手填).
        maker:
          !record.maker && maker === String(record.maker_resolved || '')
            ? ''
            : maker,
        release_date: releaseDate.trim(),
      };
      const response = await API.put('/api/model_info/model', body);
      if (!response?.data?.success) {
        throw new Error(
          response?.data?.message ||
            t(`${ns}.saveFailed`, { defaultValue: '资料没有保存' }),
        );
      }
      queryClient.invalidateQueries({ queryKey: ['console'] });
      return true;
    } catch (error) {
      showError(error);
      return false;
    }
  };
  saveRef.current = dirty ? save : null;

  const source = (field: string, fallback = '') =>
    infoSourceLabel(sources[field], t) || fallback;

  const row = (
    label: ReactNode,
    value: ReactNode,
    sourceText: string,
    key: string,
  ) => (
    <div className='mo-info__row' key={key}>
      <span className='mo-info__label'>{label}</span>
      <span className='mo-info__value'>{value}</span>
      <span className='mo-info__src'>{sourceText}</span>
    </div>
  );

  const makerOptions = (makers.data?.makers || []).map((item) => item.key);

  return (
    <>
      <div className='mo-info'>
        {row(
          t(`${ns}.field.maker`, { defaultValue: '厂商' }),
          <Select
            aria-label={t(`${ns}.field.maker`, { defaultValue: '厂商' })}
            size='sm'
            value={maker || 'none'}
            onValueChange={(value) => setMaker(value === 'none' ? '' : value)}
          >
            <SelectItem value='none'>
              {t(`${ns}.makerNone`, { defaultValue: '未归类' })}
            </SelectItem>
            {makerOptions.map((key) => (
              <SelectItem key={key} value={key}>
                {makerFullLabel(key, t)}
              </SelectItem>
            ))}
          </Select>,
          source(
            'maker',
            record.maker
              ? ''
              : t(`${ns}.source.inferred`, { defaultValue: '推断' }),
          ),
          'maker',
        )}
        {row(
          <label htmlFor='mo-info-release'>
            {t(`${ns}.field.release_date`, { defaultValue: '发布日期' })}
          </label>,
          <Input
            id='mo-info-release'
            placeholder='YYYY-MM-DD'
            value={releaseDate}
            onChange={(event) => setReleaseDate(event.target.value)}
          />,
          source('release_date'),
          'release',
        )}
        {releaseReview ? (
          <div className='mo-info__conflict' role='note'>
            <TriangleAlert aria-hidden='true' size={16} />
            <div>
              <b>
                {t(`${ns}.conflict.title`, {
                  defaultValue: 'models.dev 记的是 {{date}}',
                  date: releaseReview.upstream_value,
                })}
              </b>
              <p>
                {releaseReview.kind === 'first_run'
                  ? t(`${ns}.conflict.firstRun`, {
                      count: Number(releaseReview.note) || 0,
                      defaultValue:
                        '差了 {{count}} 天。这里原来记的多半是上架那天。',
                    })
                  : t(`${ns}.conflict.diff`, {
                      defaultValue:
                        '这个名字可能已经指向新版本，也可能是上游记错了。',
                    })}
              </p>
              <div className='mo-info__conflict-actions'>
                <Button
                  disabled={resolveMutation.isPending}
                  size='xs'
                  variant='secondary'
                  onClick={() =>
                    resolveMutation.mutate({
                      action: 'accept',
                      id: releaseReview.id,
                    })
                  }
                >
                  {t(`${ns}.conflict.accept`, {
                    defaultValue: '改成 {{date}}',
                    date: releaseReview.upstream_value,
                  })}
                </Button>
                <Button
                  disabled={resolveMutation.isPending}
                  size='xs'
                  variant='secondary'
                  onClick={() =>
                    resolveMutation.mutate({
                      action: 'keep',
                      id: releaseReview.id,
                    })
                  }
                >
                  {t(`${ns}.conflict.keep`, {
                    defaultValue: '保留，不再提示',
                  })}
                </Button>
              </div>
            </div>
          </div>
        ) : null}
      </div>
    </>
  );
}
