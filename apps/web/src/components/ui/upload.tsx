import { FileUp, X } from 'lucide-react';
import React, { useMemo, useState } from 'react';
import { useDropzone } from 'react-dropzone';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { IconButton } from './icon-button';
import { cn } from '../../lib/cn';

type DropzoneOptions = NonNullable<Parameters<typeof useDropzone>[0]>;
type UploadRenderArgs = {
  disabled: boolean;
  files: File[];
  isDragActive: boolean;
  isDragReject: boolean;
  open: () => void;
};

type UploadProps = Omit<
  DropzoneOptions,
  'disabled' | 'maxFiles' | 'maxSize' | 'multiple' | 'onDrop'
> & {
  browseLabel?: React.ReactNode;
  children?: React.ReactNode | ((args: UploadRenderArgs) => React.ReactNode);
  className?: string;
  defaultValue?: File[];
  description?: React.ReactNode;
  disabled?: boolean;
  maxFiles?: number;
  maxSize?: number;
  multiple?: boolean;
  onChange?: (files: File[]) => void;
  onFilesChange?: (files: File[]) => void;
  showFileList?: boolean;
  title?: React.ReactNode;
  value?: File[];
};

const formatBytes = (bytes: number) => {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const index = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  );
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
};

export function Upload({
  accept,
  browseLabel,
  children,
  className,
  defaultValue = [],
  description,
  disabled = false,
  maxFiles,
  maxSize,
  multiple = false,
  onChange,
  onFilesChange,
  showFileList = true,
  title,
  value,
  ...dropzoneOptions
}: UploadProps) {
  const { t } = useTranslation();
  const isControlled = value !== undefined;
  const [internalFiles, setInternalFiles] = useState<File[]>(defaultValue);
  const files = isControlled ? value || [] : internalFiles;

  const updateFiles = (nextFiles: File[]) => {
    if (!isControlled) {
      setInternalFiles(nextFiles);
    }
    onChange?.(nextFiles);
    onFilesChange?.(nextFiles);
  };

  const { getInputProps, getRootProps, isDragActive, isDragReject, open } =
    useDropzone({
      accept,
      disabled,
      maxFiles,
      maxSize,
      multiple,
      onDrop: (acceptedFiles) => {
        const nextFiles = multiple
          ? [...files, ...acceptedFiles]
          : acceptedFiles;
        updateFiles(maxFiles ? nextFiles.slice(0, maxFiles) : nextFiles);
      },
      ...dropzoneOptions,
    });

  const helperText = useMemo(() => {
    if (description) return description;
    if (maxSize) {
      return t('legacy.i18n.max_per_file.44df9e53b6', {
        size: formatBytes(maxSize),
      });
    }
    return t('legacy.i18n.drop_files_here_or_click_to_choose_files.431cc54978');
  }, [description, maxSize, t]);

  const removeFile = (fileToRemove: File) => {
    updateFiles(files.filter((file) => file !== fileToRemove));
  };

  const customChildren =
    typeof children === 'function'
      ? children({ disabled, files, isDragActive, isDragReject, open })
      : children;

  return (
    <div className={cn('lazu-upload', className)}>
      <div
        {...getRootProps({
          className: cn(
            'lazu-upload__dropzone',
            isDragActive && 'is-active',
            isDragReject && 'is-rejected',
            disabled && 'is-disabled',
          ),
        })}
      >
        <input {...getInputProps()} />
        {customChildren || (
          <>
            <div className='lazu-upload__icon'>
              <FileUp size={20} />
            </div>
            <div className='lazu-upload__copy'>
              <div className='lazu-upload__title'>
                {title || t('legacy.i18n.upload_file.484a03ac6d')}
              </div>
              <div className='lazu-upload__description'>{helperText}</div>
            </div>
            <Button
              disabled={disabled}
              onClick={(event) => {
                event.stopPropagation();
                open();
              }}
              size='sm'
              type='button'
              variant='secondary'
            >
              {browseLabel || t('legacy.i18n.choose_file.21a6f5a8d9')}
            </Button>
          </>
        )}
      </div>

      {showFileList && files.length ? (
        <ul className='lazu-upload__list'>
          {files.map((file, index) => (
            <li key={`${file.name}-${file.size}-${index}`}>
              <span className='lazu-upload__file-name'>{file.name}</span>
              <span className='lazu-upload__file-size'>
                {formatBytes(file.size)}
              </span>
              <IconButton
                aria-label={t('legacy.i18n.delete.3755f56f2f')}
                onClick={() => removeFile(file)}
                size='xs'
                type='button'
                variant='ghost'
              >
                <X size={12} />
              </IconButton>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
