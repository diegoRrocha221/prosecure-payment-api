package authorizenet

import "prosecure-payment-api/types"

type createTransactionRequestWrapper struct {
    CreateTransactionRequest createTransactionRequest `json:"createTransactionRequest"`
}

type createTransactionRequest struct {
    MerchantAuthentication merchantAuthenticationType `json:"merchantAuthentication"`
    RefID                 string                    `json:"refId,omitempty"`
    TransactionRequest    transactionRequestType    `json:"transactionRequest"`
}

type merchantAuthenticationType struct {
    Name           string `json:"name"`
    TransactionKey string `json:"transactionKey"`
}

type CreditCardType struct {
    CardNumber     string `json:"cardNumber"`
    ExpirationDate string `json:"expirationDate"`
    CardCode       string `json:"cardCode"`
}

type PaymentType struct {
    CreditCard CreditCardType `json:"creditCard"`
}

type transactionRequestType struct {
    TransactionType    string                   `json:"transactionType"`
    Amount            string                   `json:"amount,omitempty"`
    Payment           *PaymentType             `json:"payment,omitempty"`
    RefTransId        string                   `json:"refTransId,omitempty"`
    Order             *OrderType               `json:"order,omitempty"`
    Customer          *CustomerType            `json:"customer,omitempty"`
    BillTo            *types.BillingInfoType    `json:"billTo,omitempty"`
    TransactionSettings *TransactionSettingsType `json:"transactionSettings,omitempty"`
}

// TransactionSettingsType representa configurações para a transação
type TransactionSettingsType struct {
    Settings []SettingType `json:"setting,omitempty"`
}

// SettingType representa um par nome/valor para configuração de transação
type SettingType struct {
    SettingName  string `json:"settingName"`
    SettingValue string `json:"settingValue"`
}

type MessageType struct {
    Code        string `json:"code"`
    Text        string `json:"text"`
    Description string `json:"description,omitempty"`
}

type MessagesType struct {
    ResultCode string        `json:"resultCode"`
    Message    []MessageType `json:"message"`
}

type ErrorType struct {
    ErrorCode              string `json:"errorCode"`
    ErrorText              string `json:"errorText"`
    OriginalTransactionID  string `json:"originalTransactionId,omitempty"`
}

type transactionResponse struct {
    ResponseCode   string        `json:"responseCode"`
    AuthCode      string        `json:"authCode"`
    AVSResultCode string        `json:"avsResultCode"`
    CVVResultCode string        `json:"cvvResultCode"`
    TransID       string        `json:"transId"`
    RefTransID    string        `json:"refTransId"`
    Messages      []MessageType `json:"messages,omitempty"`
    Errors        []ErrorType  `json:"errors,omitempty"`
}

type createTransactionResponse struct {
    TransactionResponse transactionResponse `json:"transactionResponse"`
    Messages          MessagesType        `json:"messages"`
}

// ARB Types (Original - usando dados de cartão diretos)
type ARBSubscriptionRequest struct {
    MerchantAuthentication merchantAuthenticationType `json:"merchantAuthentication"`
    RefID                 string                    `json:"refId"`
    Subscription         ARBSubscriptionType       `json:"subscription"`
}

type ARBSubscriptionType struct {
    Name            string             `json:"name"`
    PaymentSchedule PaymentScheduleType `json:"paymentSchedule"`
    Amount         string             `json:"amount"`
    Payment        PaymentType        `json:"payment"`
    Order          OrderType          `json:"order"`
    Customer       CustomerType       `json:"customer"`
    BillTo         CustomerAddressType `json:"billTo"`
}

type PaymentScheduleType struct {
    Interval         IntervalType `json:"interval"`
    StartDate       string       `json:"startDate"`
    TotalOccurrences string       `json:"totalOccurrences"`
}

type IntervalType struct {
    Length int    `json:"length"`
    Unit   string `json:"unit"`
}

type CustomerType struct {
    Type        string `json:"type"`
    Email       string `json:"email,omitempty"`
    PhoneNumber string `json:"phoneNumber,omitempty"`
}

type OrderType struct {
    InvoiceNumber string `json:"invoiceNumber"`
    Description   string `json:"description"`
}

type CustomerAddressType struct {
    FirstName string `json:"firstName"`
    LastName  string `json:"lastName"`
    Address   string `json:"address"`
    City      string `json:"city"`
    State     string `json:"state"`
    Zip       string `json:"zip"`
    Country   string `json:"country"`
}

// ARB Response Types
type ARBResponse struct {
    RefID         string       `json:"refId"`
    SubscriptionID string       `json:"subscriptionId,omitempty"`
    Messages      MessagesType `json:"messages"`
}

// ==============================================
// CIM (Customer Information Manager) TYPES
// ==============================================

// CreateCustomerProfileRequest representa a requisição para criar um customer profile
type CreateCustomerProfileRequest struct {
    MerchantAuthentication merchantAuthenticationType `json:"merchantAuthentication"`
    RefID                 string                    `json:"refId,omitempty"`
    Profile               CustomerProfileType       `json:"profile"`
    ValidationMode        string                    `json:"validationMode,omitempty"`
}

// CreateCustomerProfileWrapper é o wrapper para a requisição
type CreateCustomerProfileRequestWrapper struct {
    CreateCustomerProfileRequest CreateCustomerProfileRequest `json:"createCustomerProfileRequest"`
}

// CustomerProfileType representa um perfil de cliente
type CustomerProfileType struct {
    MerchantCustomerID string                     `json:"merchantCustomerId,omitempty"`
    Description       string                     `json:"description,omitempty"`
    Email             string                     `json:"email,omitempty"`
    PaymentProfiles   []CustomerPaymentProfileType `json:"paymentProfiles,omitempty"`
    ShipToList        []CustomerAddressType      `json:"shipToList,omitempty"`
}

// CustomerPaymentProfileType representa um perfil de pagamento
type CustomerPaymentProfileType struct {
    CustomerType            string                `json:"customerType,omitempty"`
    BillTo                  *CustomerAddressType  `json:"billTo,omitempty"`
    Payment                 *PaymentType          `json:"payment,omitempty"`
    DefaultPaymentProfile   bool                  `json:"defaultPaymentProfile,omitempty"`
}

// CreateCustomerProfileResponse representa a resposta da criação do profile
type CreateCustomerProfileResponse struct {
    Messages                      MessagesType `json:"messages"`
    CustomerProfileID            string       `json:"customerProfileId,omitempty"`
    CustomerPaymentProfileIDList []string     `json:"customerPaymentProfileIdList,omitempty"`
    CustomerShippingAddressIDList []string    `json:"customerShippingAddressIdList,omitempty"`
    ValidationDirectResponseList []string     `json:"validationDirectResponseList,omitempty"`
}

// ARBSubscriptionTypeWithProfile é a versão do ARB que usa Customer Profile
type ARBSubscriptionTypeWithProfile struct {
    Name            string             `json:"name"`
    PaymentSchedule PaymentScheduleType `json:"paymentSchedule"`
    Amount         string             `json:"amount"`
    Profile        ProfileType        `json:"profile"`
    Order          OrderType          `json:"order"`
    Customer       CustomerType       `json:"customer"`
    BillTo         CustomerAddressType `json:"billTo"`
}

// ProfileType representa a referência ao customer profile para ARB
type ProfileType struct {
    CustomerProfileID       string `json:"customerProfileId"`
    CustomerPaymentProfileID string `json:"customerPaymentProfileId"`
    CustomerAddressID       string `json:"customerAddressId,omitempty"`
}

// ARBSubscriptionRequestWithProfile é a requisição ARB usando Customer Profile
type ARBSubscriptionRequestWithProfile struct {
    MerchantAuthentication merchantAuthenticationType     `json:"merchantAuthentication"`
    RefID                 string                        `json:"refId"`
    Subscription         ARBSubscriptionTypeWithProfile `json:"subscription"`
}

// GetCustomerProfileRequest para buscar um perfil existente
type GetCustomerProfileRequest struct {
    MerchantAuthentication merchantAuthenticationType `json:"merchantAuthentication"`
    CustomerProfileID     string                    `json:"customerProfileId"`
    UnmaskExpirationDate  bool                      `json:"unmaskExpirationDate,omitempty"`
    IncludeIssuerInfo     bool                      `json:"includeIssuerInfo,omitempty"`
}

// GetCustomerProfileRequestWrapper
type GetCustomerProfileRequestWrapper struct {
    GetCustomerProfileRequest GetCustomerProfileRequest `json:"getCustomerProfileRequest"`
}

// GetCustomerProfileResponse 
type GetCustomerProfileResponse struct {
    Messages MessagesType        `json:"messages"`
    Profile  CustomerProfileType `json:"profile,omitempty"`
}

// UpdateCustomerPaymentProfileRequest para atualizar método de pagamento
type UpdateCustomerPaymentProfileRequest struct {
    MerchantAuthentication merchantAuthenticationType  `json:"merchantAuthentication"`
    CustomerProfileID     string                     `json:"customerProfileId"`
    PaymentProfile        CustomerPaymentProfileType `json:"paymentProfile"`
    ValidationMode        string                     `json:"validationMode,omitempty"`
}

// UpdateCustomerPaymentProfileRequestWrapper
type UpdateCustomerPaymentProfileRequestWrapper struct {
    UpdateCustomerPaymentProfileRequest UpdateCustomerPaymentProfileRequest `json:"updateCustomerPaymentProfileRequest"`
}

// UpdateCustomerPaymentProfileResponse
type UpdateCustomerPaymentProfileResponse struct {
    Messages                     MessagesType `json:"messages"`
    ValidationDirectResponse    string       `json:"validationDirectResponse,omitempty"`
}