/** The languages a profile can name, by the two-letter code the probe stores. A code outside the list (an older
 *  profile's) is still shown, by its code. */
export const LANGUAGES: readonly { code: string; name: string }[] = [
  { code: "en", name: "English" }, { code: "de", name: "German" }, { code: "fr", name: "French" },
  { code: "es", name: "Spanish" }, { code: "it", name: "Italian" }, { code: "pt", name: "Portuguese" },
  { code: "nl", name: "Dutch" }, { code: "sv", name: "Swedish" }, { code: "no", name: "Norwegian" },
  { code: "da", name: "Danish" }, { code: "fi", name: "Finnish" }, { code: "is", name: "Icelandic" },
  { code: "pl", name: "Polish" }, { code: "cs", name: "Czech" }, { code: "sk", name: "Slovak" },
  { code: "hu", name: "Hungarian" }, { code: "ro", name: "Romanian" }, { code: "bg", name: "Bulgarian" },
  { code: "el", name: "Greek" }, { code: "tr", name: "Turkish" }, { code: "ru", name: "Russian" },
  { code: "uk", name: "Ukrainian" }, { code: "sr", name: "Serbian" }, { code: "hr", name: "Croatian" },
  { code: "ja", name: "Japanese" }, { code: "ko", name: "Korean" }, { code: "zh", name: "Chinese" },
  { code: "hi", name: "Hindi" }, { code: "ar", name: "Arabic" }, { code: "he", name: "Hebrew" },
  { code: "fa", name: "Persian" }, { code: "th", name: "Thai" }, { code: "vi", name: "Vietnamese" },
  { code: "id", name: "Indonesian" }, { code: "ca", name: "Catalan" }, { code: "la", name: "Latin" },
];

export function languageName(code: string): string {
  return LANGUAGES.find((language) => language.code === code.toLowerCase())?.name ?? code;
}
