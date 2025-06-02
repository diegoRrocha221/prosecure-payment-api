package email

const (
    ActivationEmailTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Welcome to ProSecureLSP</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap');
        
        /* Reset styles for email clients */
        body, table, td, p, a, li, blockquote {
            -webkit-text-size-adjust: 100%%;
            -ms-text-size-adjust: 100%%;
        }
        
        table, td {
            mso-table-lspace: 0pt;
            mso-table-rspace: 0pt;
        }
        
        img {
            -ms-interpolation-mode: bicubic;
            border: 0;
            height: auto;
            line-height: 100%%;
            outline: none;
            text-decoration: none;
        }
        
        /* Responsive styles */
        @media only screen and (max-width: 600px) {
            .container {
                width: 100%% !important;
                max-width: 100%% !important;
            }
            
            .content-padding {
                padding: 20px !important;
            }
            
            .mobile-center {
                text-align: center !important;
            }
            
            .mobile-full-width {
                width: 100%% !important;
                display: block !important;
            }
        }
    </style>
</head>
<body style="margin: 0; padding: 0; background-color: #f9fafb; font-family: 'Inter', Arial, sans-serif;">
    <!-- Main Container -->
    <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%" style="background-color: #f9fafb;">
        <tr>
            <td align="center" style="padding: 40px 20px;">
                
                <!-- Email Content Container -->
                <table role="presentation" class="container" cellspacing="0" cellpadding="0" border="0" width="600" style="max-width: 600px; background-color: #ffffff; border-radius: 12px; box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1); overflow: hidden;">
                    
                    <!-- Header -->
                    <tr>
                        <td style="background-color: #25364D; padding: 32px 20px; text-align: center;">
                            <img src="https://prosecurelsp.com/images/logo.png" alt="ProSecureLSP Logo" style="height: 48px; width: auto; display: inline-block;">
                        </td>
                    </tr>
                    
                    <!-- Main Content -->
                    <tr>
                        <td class="content-padding" style="padding: 48px 40px;">
                            
                            <!-- Welcome Section -->
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td style="text-align: center; padding-bottom: 32px;">
                                        <div style="width: 64px; height: 64px; background-color: #dcfdf7; border-radius: 50%%; display: inline-flex; align-items: center; justify-content: center; margin-bottom: 24px;">
                                            <svg style="width: 32px; height: 32px; color: #157347;" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 12a4 4 0 10-8 0 4 4 0 008 0zm0 0v1.5a2.5 2.5 0 005 0V12a9 9 0 10-9 9m4.5-1.206a8.959 8.959 0 01-4.5 1.207"></path>
                                            </svg>
                                        </div>
                                        <h1 style="color: #25364D; font-size: 28px; font-weight: 700; margin: 0 0 8px 0; line-height: 1.2;">
                                            Welcome to ProSecureLSP!
                                        </h1>
                                        <p style="color: #6b7280; font-size: 16px; margin: 0; line-height: 1.5;">
                                            Hi %s! Please confirm your email address to get started.
                                        </p>
                                    </td>
                                </tr>
                            </table>
                            
                            <!-- Message Content -->
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td style="padding-bottom: 32px;">
                                        <h3 style="color: #25364D; font-size: 18px; font-weight: 600; margin: 0 0 16px 0;">
                                            Please Confirm Your Email Address
                                        </h3>
                                        <p style="color: #374151; font-size: 16px; line-height: 1.6; margin: 0 0 16px 0;">
                                            %s
                                        </p>
                                        <p style="color: #374151; font-size: 16px; line-height: 1.6; margin: 0 0 16px 0;">
                                            Once we confirm your email, you'll be able to log into your Administrator Portal and begin setting up your devices on the most advanced security service on the planet.
                                        </p>
                                        <p style="color: #374151; font-size: 16px; line-height: 1.6; margin: 0;">
                                            Simply click the button below to verify your account and get started.
                                        </p>
                                    </td>
                                </tr>
                            </table>
                            
                            <!-- Action Button -->
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td style="text-align: center; padding: 32px 0;">
                                        <a href="%s" style="display: inline-block; background-color: #157347; color: #ffffff; text-decoration: none; font-weight: 600; font-size: 16px; padding: 16px 32px; border-radius: 8px; transition: background-color 0.2s;">
                                            Confirm Email Address
                                        </a>
                                    </td>
                                </tr>
                            </table>
                            
                            <!-- Secondary Info -->
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td style="text-align: center; padding-top: 24px; border-top: 1px solid #e5e7eb;">
                                        <p style="color: #6b7280; font-size: 14px; margin: 0 0 16px 0;">
                                            If the button doesn't work, you can copy and paste this link into your browser:
                                        </p>
                                        <p style="color: #157347; font-size: 14px; margin: 0; word-break: break-all;">
                                            %s
                                        </p>
                                    </td>
                                </tr>
                            </table>
                            
                        </td>
                    </tr>
                    
                    <!-- Footer -->
                    <tr>
                        <td style="background-color: #25364D; padding: 32px 40px; text-align: center;">
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td style="text-align: center; padding-bottom: 16px;">
                                        <p style="color: #9ca3af; font-size: 14px; margin: 0; line-height: 1.5;">
                                            %s
                                        </p>
                                    </td>
                                </tr>
                                <tr>
                                    <td style="text-align: center;">
                                        <p style="color: #9ca3af; font-size: 12px; margin: 0;">
                                            © 2025 ProSecureLSP. All rights reserved.
                                        </p>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>
                    
                </table>
                
            </td>
        </tr>
    </table>
</body>
</html>`

    InvoiceEmailTemplate = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Your Invoice from ProSecureLSP</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap');
        
        /* Reset styles for email clients */
        body, table, td, p, a, li, blockquote {
            -webkit-text-size-adjust: 100%%;
            -ms-text-size-adjust: 100%%;
        }
        
        table, td {
            mso-table-lspace: 0pt;
            mso-table-rspace: 0pt;
        }
        
        img {
            -ms-interpolation-mode: bicubic;
            border: 0;
            height: auto;
            line-height: 100%%;
            outline: none;
            text-decoration: none;
        }
        
        /* Invoice specific styles */
        .invoice-table {
            width: 100%%;
            border-collapse: collapse;
            margin: 16px 0;
        }
        
        .invoice-table th {
            background-color: #25364D;
            color: #ffffff;
            padding: 12px;
            text-align: left;
            font-weight: 600;
            font-size: 14px;
        }
        
        .invoice-table td {
            padding: 12px;
            border-bottom: 1px solid #e5e7eb;
            font-size: 14px;
            color: #374151;
        }
        
        .invoice-table tr:nth-child(even) {
            background-color: #f9fafb;
        }
        
        /* Responsive styles */
        @media only screen and (max-width: 600px) {
            .container {
                width: 100%% !important;
                max-width: 100%% !important;
            }
            
            .content-padding {
                padding: 20px !important;
            }
            
            .mobile-center {
                text-align: center !important;
            }
            
            .mobile-full-width {
                width: 100%% !important;
                display: block !important;
            }
            
            .invoice-table th,
            .invoice-table td {
                padding: 8px !important;
                font-size: 13px !important;
            }
        }
    </style>
</head>
<body style="margin: 0; padding: 0; background-color: #f9fafb; font-family: 'Inter', Arial, sans-serif;">
    <!-- Main Container -->
    <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%" style="background-color: #f9fafb;">
        <tr>
            <td align="center" style="padding: 40px 20px;">
                
                <!-- Email Content Container -->
                <table role="presentation" class="container" cellspacing="0" cellpadding="0" border="0" width="600" style="max-width: 600px; background-color: #ffffff; border-radius: 12px; box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1); overflow: hidden;">
                    
                    <!-- Header -->
                    <tr>
                        <td style="background-color: #25364D; padding: 32px 20px; text-align: center;">
                            <img src="https://prosecurelsp.com/images/logo.png" alt="ProSecureLSP Logo" style="height: 48px; width: auto; display: inline-block;">
                        </td>
                    </tr>
                    
                    <!-- Invoice Header -->
                    <tr>
                        <td class="content-padding" style="padding: 32px 40px 16px 40px;">
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td>
                                        <h1 style="color: #25364D; font-size: 28px; font-weight: 700; margin: 0 0 8px 0; line-height: 1.2;">
                                            Invoice Delivered
                                        </h1>
                                        <p style="color: #6b7280; font-size: 16px; margin: 0 0 24px 0;">
                                            Your ProSecureLSP subscription is now active
                                        </p>
                                    </td>
                                    <td style="text-align: right; vertical-align: top;">
                                        <div style="background-color: #f9fafb; padding: 16px; border-radius: 8px; min-width: 150px;">
                                            <p style="color: #25364D; font-size: 14px; font-weight: 600; margin: 0 0 4px 0;">
                                                Invoice #%s
                                            </p>
                                            <p style="color: #6b7280; font-size: 12px; margin: 0;">
                                                Issued Today
                                            </p>
                                        </div>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>
                    
                    <!-- Plans Table -->
                    <tr>
                        <td style="padding: 16px 40px;">
                            <h3 style="color: #25364D; font-size: 18px; font-weight: 600; margin: 0 0 16px 0;">
                                Selected Plans
                            </h3>
                            %s
                        </td>
                    </tr>
                    
                    <!-- Totals Section -->
                    <tr>
                        <td style="padding: 16px 40px 32px 40px;">
                            <div style="background-color: #f9fafb; padding: 24px; border-radius: 8px; border-left: 4px solid #157347;">
                                %s
                                <div style="margin-top: 16px; padding-top: 16px; border-top: 2px solid #25364D;">
                                    <p style="color: #25364D; font-size: 18px; font-weight: 700; margin: 0;">
                                        <strong>Status:</strong> <span style="color: #157347;">%s</span>
                                    </p>
                                </div>
                            </div>
                        </td>
                    </tr>
                    
                    <!-- Message Content -->
                    <tr>
                        <td style="padding: 0 40px 32px 40px;">
                            <div style="background-color: #dcfdf7; padding: 20px; border-radius: 8px; border-left: 4px solid #157347;">
                                <p style="color: #374151; font-size: 16px; line-height: 1.6; margin: 0;">
                                    🎉 <strong>Welcome to ProSecureLSP!</strong> Your account is now active and ready to use. You can access your Administrator Portal immediately to begin configuring your advanced security services.
                                </p>
                            </div>
                        </td>
                    </tr>
                    
                    <!-- Action Button -->
                    <tr>
                        <td style="padding: 0 40px 32px 40px; text-align: center;">
                            <a href="https://prosecurelsp.com/users/index.php" style="display: inline-block; background-color: #157347; color: #ffffff; text-decoration: none; font-weight: 600; font-size: 16px; padding: 16px 32px; border-radius: 8px; transition: background-color 0.2s;">
                                Access Your Portal
                            </a>
                        </td>
                    </tr>
                    
                    <!-- Footer Message -->
                    <tr>
                        <td style="padding: 0 40px 32px 40px;">
                            <p style="color: #374151; font-size: 14px; line-height: 1.6; margin: 0; text-align: center;">
                                %s
                            </p>
                        </td>
                    </tr>
                    
                    <!-- Footer -->
                    <tr>
                        <td style="background-color: #25364D; padding: 32px 40px; text-align: center;">
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                <tr>
                                    <td style="text-align: center; padding-bottom: 16px;">
                                        <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%%">
                                            <tr>
                                                <td style="text-align: center;">
                                                    <a href="https://prosecurelsp.com/contact.php" style="color: #9ca3af; text-decoration: none; font-size: 12px; margin: 0 8px;">
                                                        Support Center
                                                    </a>
                                                    <span style="color: #6b7280;">|</span>
                                                    <a href="https://prosecurelsp.com/users/index.php" style="color: #9ca3af; text-decoration: none; font-size: 12px; margin: 0 8px;">
                                                        Billing
                                                    </a>
                                                    <span style="color: #6b7280;">|</span>
                                                    <a href="https://prosecurelsp.com/contact.php" style="color: #9ca3af; text-decoration: none; font-size: 12px; margin: 0 8px;">
                                                        Contact Us
                                                    </a>
                                                </td>
                                            </tr>
                                        </table>
                                    </td>
                                </tr>
                                <tr>
                                    <td style="text-align: center;">
                                        <p style="color: #9ca3af; font-size: 12px; margin: 0;">
                                            © 2025 ProSecureLSP. All rights reserved.
                                        </p>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>
                    
                </table>
                
            </td>
        </tr>
    </table>
</body>
</html>`
)