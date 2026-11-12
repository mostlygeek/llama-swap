const ACCEPTED_IMAGE_FORMATS = ["image/jpeg", "image/png", "image/gif", "image/webp"];
const MAX_IMAGE_SIZE = 20 * 1024 * 1024;
const MAX_IMAGES_PER_MESSAGE = 5;
const imageLimitError = `Maximum ${MAX_IMAGES_PER_MESSAGE} images per message`;

type ImagePasteEvent = {
  clipboardData: { files: ArrayLike<File> } | null;
  preventDefault(): void;
};

export function pasteImageFiles(event: ImagePasteEvent, attach: (files: File[]) => void) {
  if (!event.clipboardData) return;
  const files = Array.from(event.clipboardData.files).filter((file) => file.type.startsWith("image/"));
  if (files.length === 0) return;
  event.preventDefault();
  attach(files);
}

function fileToDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () => reject(new Error("Failed to read file"));
    reader.readAsDataURL(file);
  });
}

export async function appendImageFiles(
  files: readonly File[],
  currentImages: () => readonly string[],
  replaceImages: (images: string[]) => void,
  readImage: (file: File) => Promise<string> = fileToDataUrl,
): Promise<string | null> {
  if (currentImages().length + files.length > MAX_IMAGES_PER_MESSAGE) return imageLimitError;

  for (const file of files) {
    if (!ACCEPTED_IMAGE_FORMATS.includes(file.type)) {
      return `Invalid file type: ${file.type}. Accepted formats: JPG, PNG, GIF, WEBP`;
    }
    if (file.size > MAX_IMAGE_SIZE) {
      return `File too large: ${(file.size / 1024 / 1024).toFixed(1)}MB. Maximum size: 20MB`;
    }
  }

  try {
    const dataUrls = await Promise.all(files.map(readImage));
    // Another paste or file selection may have finished during these reads.
    const existing = currentImages();
    if (existing.length + dataUrls.length > MAX_IMAGES_PER_MESSAGE) return imageLimitError;
    replaceImages([...existing, ...dataUrls]);
    return null;
  } catch (error) {
    return error instanceof Error ? error.message : "Failed to process images";
  }
}
