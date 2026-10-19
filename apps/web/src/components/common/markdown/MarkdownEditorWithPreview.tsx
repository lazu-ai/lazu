import { Eye, EyeOff, Pencil } from 'lucide-react';
import React, { lazy, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useDebounce } from '../../../hooks/common/useDebounce';
import { TextArea, Spin } from '@/components/ui/console-primitives';

const MarkdownRenderer = lazy(() => import('./MarkdownRenderer'));

type LegacyProps = {
  children?: React.ReactNode;
  [key: string]: unknown;
};

const LegacyTextArea = TextArea as unknown as React.ComponentType<LegacyProps>;
const LegacySpin = Spin as unknown as React.ComponentType<LegacyProps>;

type MarkdownEditorWithPreviewProps = {
  className?: string;
  maxCount?: number;
  onChange?: (value: string) => void;
  placeholder?: string;
  rows?: number;
  value?: string;
};

/**
 * MarkdownEditorWithPreview - 带实时预览的 Markdown 编辑器
 *
 * 默认左右分栏：左侧编辑区、右侧实时预览
 * 支持收起预览以获得更大编辑区域
 */
const MarkdownEditorWithPreview = ({
  value = '',
  onChange,
  placeholder = '',
  maxCount,
  rows = 8,
  className = '',
}: MarkdownEditorWithPreviewProps) => {
  const { t } = useTranslation();
  const [showPreview, setShowPreview] = useState(true);
  // 防抖处理，300ms 延迟渲染预览，减少性能开销
  const debouncedValue = useDebounce(value, 300);

  return (
    <div className={`flex flex-col gap-2 ${className}`}>
      <div className='flex gap-4 w-full items-stretch'>
        {/* 左侧 - 编辑区 */}
        <div
          className={`flex flex-col min-w-0 ${showPreview ? 'flex-1' : 'w-full'}`}
        >
          <div className='flex items-center justify-between mb-1.5'>
            <div className='flex items-center gap-1.5 text-xs font-medium text-[var(--ui-text-faint)]'>
              <Pencil size={12} />
              <span>Markdown</span>
            </div>
            <button
              type='button'
              className='flex items-center gap-1 text-xs text-[var(--ui-text-faint)] hover:text-[var(--ui-accent)] cursor-pointer bg-transparent border-none p-0'
              onClick={() => setShowPreview(!showPreview)}
            >
              {showPreview ? <EyeOff size={12} /> : <Eye size={12} />}
              <span>
                {showPreview
                  ? t('legacy.i18n.hide_preview.5bcf7b434e')
                  : t('legacy.i18n.show_preview.cbb1f5c6c3')}
              </span>
            </button>
          </div>
          <LegacyTextArea
            value={value}
            placeholder={placeholder}
            maxCount={maxCount}
            className='font-mono !text-sm'
            autosize={{ minRows: rows, maxRows: rows * 3 }}
            onChange={onChange}
          />
        </div>

        {/* 右侧 - 预览区 */}
        {showPreview && (
          <>
            <div className='w-px bg-[var(--ui-border)] shrink-0' />
            <div className='flex-1 flex flex-col min-w-0'>
              <div className='flex items-center gap-1.5 mb-1.5 text-xs font-medium text-[var(--ui-text-faint)]'>
                <Eye size={12} />
                <span>{t('legacy.i18n.preview.de61aa8e1c')}</span>
              </div>
              <div
                className='border border-[var(--ui-border)] rounded-lg p-3 bg-[var(--ui-surface-muted)] overflow-y-auto'
                style={{
                  minHeight: `${rows * 1.5}rem`,
                  maxHeight: `${rows * 4.5}rem`,
                }}
              >
                {debouncedValue ? (
                  <Suspense
                    fallback={
                      <div className='flex items-center justify-center py-8'>
                        <LegacySpin size='middle' />
                      </div>
                    }
                  >
                    <MarkdownRenderer content={debouncedValue} fontSize={14} />
                  </Suspense>
                ) : (
                  <div
                    className='flex items-center justify-center text-[var(--ui-text-faint)] text-xs'
                    style={{ minHeight: `${rows * 1.5}rem` }}
                  >
                    <div className='text-center opacity-50'>
                      <Eye size={24} className='mx-auto mb-1.5' />
                      <div>
                        {t(
                          'legacy.i18n.enter_content_on_the_left_to_see_a_real_time_preview_here.5c250dfe0e',
                        )}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
};

export default MarkdownEditorWithPreview;
