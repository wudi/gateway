interface EmptyStateProps {
  message: string;
  docsLink?: string;
}

export function EmptyState({ message, docsLink }: EmptyStateProps) {
  return (
    <div className="py-8 text-center text-text-secondary text-sm">
      <p>{message}</p>
      {docsLink && (
        <a
          href={docsLink}
          className="mt-2 inline-block text-blue-400 hover:text-blue-300 transition-colors duration-150"
          target="_blank"
          rel="noopener noreferrer"
        >
          View documentation
        </a>
      )}
    </div>
  );
}
