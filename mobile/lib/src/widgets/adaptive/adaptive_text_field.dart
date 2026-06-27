import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'platform.dart';

BoxDecoration _cupertinoFieldDecoration(BuildContext context) {
  return BoxDecoration(
    color: CupertinoDynamicColor.resolve(
      CupertinoColors.tertiarySystemFill,
      context,
    ),
    borderRadius: BorderRadius.circular(10),
  );
}

/// A text input rendered natively per platform: [CupertinoTextField] on iOS,
/// Material [TextField] on Android.
///
/// [label] is the Material floating label; [placeholder] (falling back to
/// [label]) is the iOS placeholder / Material hint. Supports multi-line input
/// via [minLines]/[maxLines].
class AdaptiveTextField extends StatelessWidget {
  const AdaptiveTextField({
    super.key,
    required this.controller,
    this.label,
    this.placeholder,
    this.obscureText = false,
    this.keyboardType,
    this.textInputAction,
    this.autofocus = false,
    this.textAlign = TextAlign.start,
    this.style,
    this.maxLength,
    this.minLines,
    this.maxLines = 1,
    this.enabled = true,
    this.autofillHints,
    this.prefix,
    this.suffix,
    this.onChanged,
    this.onSubmitted,
  });

  final TextEditingController controller;
  final String? label;
  final String? placeholder;
  final bool obscureText;
  final TextInputType? keyboardType;
  final TextInputAction? textInputAction;
  final bool autofocus;
  final TextAlign textAlign;
  final TextStyle? style;
  final int? maxLength;
  final int? minLines;
  final int? maxLines;
  final bool enabled;
  final Iterable<String>? autofillHints;
  final Widget? prefix;
  final Widget? suffix;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;

  @override
  Widget build(BuildContext context) {
    if (isCupertino(context)) {
      return CupertinoTextField(
        controller: controller,
        placeholder: placeholder ?? label,
        obscureText: obscureText,
        keyboardType: keyboardType,
        textInputAction: textInputAction,
        autofocus: autofocus,
        textAlign: textAlign,
        style: style,
        minLines: minLines,
        maxLines: obscureText ? 1 : maxLines,
        enabled: enabled,
        autofillHints: autofillHints,
        prefix: prefix,
        suffix: suffix,
        onChanged: onChanged,
        onSubmitted: onSubmitted,
        padding: const EdgeInsets.all(12),
        decoration: _cupertinoFieldDecoration(context),
        inputFormatters: maxLength == null
            ? null
            : [LengthLimitingTextInputFormatter(maxLength)],
      );
    }
    return TextField(
      controller: controller,
      obscureText: obscureText,
      keyboardType: keyboardType,
      textInputAction: textInputAction,
      autofocus: autofocus,
      textAlign: textAlign,
      style: style,
      minLines: minLines,
      maxLines: obscureText ? 1 : maxLines,
      enabled: enabled,
      autofillHints: autofillHints,
      maxLength: maxLength,
      onChanged: onChanged,
      onSubmitted: onSubmitted,
      decoration: InputDecoration(
        labelText: label,
        hintText: placeholder,
        prefixIcon: prefix,
        suffixIcon: suffix,
        counterText: maxLength == null ? null : '',
      ),
    );
  }
}

/// A [Form]-integrated text input: [CupertinoTextFormFieldRow] on iOS (with
/// inline red validation text) and Material [TextFormField] on Android.
class AdaptiveTextFormField extends StatelessWidget {
  const AdaptiveTextFormField({
    super.key,
    required this.controller,
    this.label,
    this.obscureText = false,
    this.keyboardType,
    this.autofocus = false,
    this.autofillHints,
    this.validator,
    this.onChanged,
    this.onFieldSubmitted,
  });

  final TextEditingController controller;
  final String? label;
  final bool obscureText;
  final TextInputType? keyboardType;
  final bool autofocus;
  final Iterable<String>? autofillHints;
  final FormFieldValidator<String>? validator;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onFieldSubmitted;

  @override
  Widget build(BuildContext context) {
    if (isCupertino(context)) {
      return CupertinoTextFormFieldRow(
        controller: controller,
        placeholder: label,
        obscureText: obscureText,
        keyboardType: keyboardType,
        autofocus: autofocus,
        autofillHints: autofillHints,
        validator: validator,
        onChanged: onChanged,
        onFieldSubmitted: onFieldSubmitted,
        padding: const EdgeInsets.symmetric(vertical: 6),
        decoration: _cupertinoFieldDecoration(context),
      );
    }
    return TextFormField(
      controller: controller,
      obscureText: obscureText,
      keyboardType: keyboardType,
      autofocus: autofocus,
      autofillHints: autofillHints,
      validator: validator,
      onChanged: onChanged,
      onFieldSubmitted: onFieldSubmitted,
      decoration: InputDecoration(labelText: label),
    );
  }
}
